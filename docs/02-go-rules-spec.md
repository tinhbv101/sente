# 02 — Đặc tả luật cờ vây (engine specification)

> Tài liệu này là **nguồn sự thật duy nhất** cho hành vi của engine luật. Cả bản Swift
> (client) và bản Go (server) phải implement đúng tài liệu này và pass cùng bộ
> conformance vectors mô tả ở [09-testing-strategy.md](09-testing-strategy.md).
>
> Mọi thay đổi ở đây là **breaking change**: phải bump `rules_version` và cập nhật vectors.

## 1. Vì sao cần đặc tả riêng

Luật cờ vây "dễ hiểu, khó đặc tả". Ba chỗ mà hầu hết implementation nghiệp dư làm sai:

1. **Ko vs. superko** — luật ko cơ bản không chặn được vòng lặp dài (triple ko), và hai hệ
   luật xử lý khác nhau (Nhật: vô hiệu ván; Trung: cấm bởi superko).
2. **Thứ tự bắt quân và tự sát** — phải gỡ quân đối phương *trước* khi kiểm tra tự sát,
   nếu không sẽ cấm nhầm những nước hợp lệ.
3. **Đếm điểm trong seki** — theo luật Nhật, mắt trong seki không tính là đất.

Vì vậy mọi quy tắc dưới đây được viết ở dạng thuật toán, không ở dạng văn xuôi.

## 2. Mô hình dữ liệu

### 2.1 Bàn cờ

```
N = 9 | 13 | 19            // board size
Point = (col, row), 0-based, 0 <= col,row < N
Color = Empty | Black | White
```

**Biểu diễn nội bộ.** Mảng 1 chiều có viền canh (sentinel border) để loại bỏ mọi kiểm tra
biên trong vòng lặp láng giềng:

```
W       = N + 2                       // padded width
cells   = [Color; W * W]              // border cells hold a sentinel value OffBoard
index(col, row) = (row + 1) * W + (col + 1)
neighbors(i)    = [i - W, i - 1, i + 1, i + W]   // up, left, right, down
```

Chỉ dùng láng giềng **trực giao 4 hướng**. Đường chéo không nối quân.

### 2.2 Đám quân (chain)

Đám quân = tập tối đa các quân cùng màu liên thông qua láng giềng 4 hướng.
Khí (liberty) của một đám = tập giao điểm trống kề với ít nhất một quân của đám.

Đám quân **không** được lưu tường minh trong trạng thái ván (để trạng thái là dữ liệu
thuần và dễ so sánh); nó được tính bằng flood fill khi cần. Xem [§10](#10-hiệu-năng-và-tối-ưu)
về tối ưu.

### 2.3 Nước đi

```
Move =
  | Play(point)
  | Pass
  | Resign
```

### 2.4 Trạng thái ván

```
GameState {
  boardSize:     Int
  rules:         Japanese | Chinese
  komi:          Decimal            // e.g. 6.5
  handicap:      Int                // 0, or 2..9
  cells:         [Color]
  toPlay:        Black | White
  moveNumber:    Int                // starts at 0
  koPoint:       Point?             // only used by basic ko (Japanese)
  captures:      [Black: Int, White: Int]   // prisoners taken so far
  consecutivePasses: Int
  positionHistory:   Set<ZobristHash>       // superko (Chinese); cycle detection (Japanese)
  phase:         Playing | Scoring | Finished
}
```

### 2.5 Zobrist hash

Hash 64-bit của **thế cờ trên bàn** (không gồm lượt đi, không gồm số nước):

```
zobrist[point][color]  // fixed 64-bit random constants, SAME in Swift and Go
hash = XOR over all non-empty points of zobrist[point][color]
```

Hằng số Zobrist là **một phần của đặc tả**: chúng được sinh một lần, lưu trong file
`rules/zobrist_table.json` của repo dùng chung và nạp bởi cả hai implementation. Không
sinh ngẫu nhiên lúc chạy — nếu không, hash không so sánh được giữa client và server.

## 3. Luật đặt quân

### 3.1 Thuật toán kiểm tra hợp lệ

```
function isLegal(state, point, color) -> Result<Void, IllegalReason>:
    if not onBoard(point):            return .error(.outOfBounds)
    if state.cells[point] != Empty:   return .error(.occupied)
    if color != state.toPlay:         return .error(.notYourTurn)

    // Basic ko is a fast pre-check; superko is verified after simulation below.
    if state.rules == Japanese and point == state.koPoint:
        return .error(.ko)

    next = simulate(state, point, color)      // never mutates `state`

    if next.selfChainLiberties == 0:
        return .error(.suicide)

    if state.rules == Chinese and next.hash in state.positionHistory:
        return .error(.superko)

    return .ok
```

### 3.2 Thuật toán mô phỏng đặt quân

**Thứ tự các bước là bắt buộc** và là nguồn của hầu hết bug trong các implementation khác:

```
function simulate(state, point, color) -> SimResult:
    board = copy(state.cells)
    board[point] = color                       // 1. place the stone first

    captured = []
    for n in neighbors(point):                 // 2. remove opponent chains with no liberties
        if board[n] == opponent(color):
            chain = floodFill(board, n)
            if liberties(board, chain) == 0:
                captured.append(chain)
                for p in chain: board[p] = Empty

    // 3. ONLY NOW check suicide: capturing may have created liberties
    ownChain     = floodFill(board, point)
    ownLiberties = liberties(board, ownChain)

    return SimResult(board, captured, ownLiberties, zobrist(board))
```

**Ví dụ minh họa vì sao thứ tự quan trọng.** Đen đặt vào giao điểm mà đám Đen mới sẽ có 0
khí *nếu xét trước khi bắt*, nhưng nước đó bắt được một đám Trắng và nhờ vậy đám Đen có
khí. Nước đó **hợp lệ**. Nếu kiểm tra tự sát trước khi gỡ quân, engine sẽ cấm nhầm.

### 3.3 Tự sát (suicide)

Cấm tuyệt đối ở cả hai hệ luật được hỗ trợ. (Luật New Zealand/Ing cho phép tự sát nhiều
quân — **không** nằm trong phạm vi v1.0; nếu thêm sau thì là một giá trị `rules` mới, không
sửa hành vi của Japanese/Chinese.)

### 3.4 Áp dụng nước đi

```
function applyMove(state, Play(point)) -> GameState:
    sim = simulate(state, point, state.toPlay)

    newState = state với:
        cells             = sim.board
        captures[toPlay] += tổng số quân trong sim.captured
        moveNumber       += 1
        consecutivePasses = 0
        toPlay            = opponent(state.toPlay)
        positionHistory   = state.positionHistory ∪ { sim.hash }
        koPoint           = koPointAfter(sim, point)
    return newState
```

**Quan trọng (quy ước immutability của team):** `applyMove` trả về state mới, không sửa
state cũ. Điều này khiến replay, undo, và tính biến hóa (variation) trở nên tầm thường.

## 4. Luật ko

### 4.1 Ko cơ bản (dùng cho hệ luật Nhật Bản)

Một nước bị cấm bởi ko cơ bản khi nó tái tạo lại thế cờ ngay trước nước đi vừa rồi của đối
phương. Cách cài đặt tương đương và rẻ hơn — theo dõi **điểm ko**:

```
function koPointAfter(sim, playedPoint) -> Point?:
    // A ko point exists only in the classic single-stone recapture shape.
    if sim.capturedStoneCount == 1
       and sizeOf(chain containing playedPoint) == 1
       and libertiesOf(that chain) == 1:
        return the single captured point
    return nil
```

Điểm ko chỉ có hiệu lực cho **đúng một nước tiếp theo**, sau đó bị xóa.

### 4.2 Positional superko (dùng cho hệ luật Trung Quốc)

Cấm mọi nước đi tạo ra thế cờ **đã từng xuất hiện** trong ván (chỉ xét thế cờ trên bàn,
không xét ai đang đi — đây là *positional* superko, PSK).

```
if sim.hash in state.positionHistory: illegal(.superko)
```

`positionHistory` phải chứa cả thế cờ ban đầu (bàn trống, hoặc bàn đã đặt quân chấp).

> **Lưu ý cài đặt.** Dùng `Set<UInt64>`. Xác suất đụng độ hash 64-bit trong một ván 300
> nước là khoảng 2⁻⁴⁵ — chấp nhận được. Không cần lưu cả bàn cờ.

### 4.3 Lặp thế trong hệ luật Nhật (triple ko)

Luật Nhật không có superko. Thế lặp dài (ba kiếp, kiếp vòng) dẫn tới **vô hiệu ván**
(無勝負, *no result*). Cài đặt:

```
if state.rules == Japanese and sim.hash in state.positionHistory:
    // Legal move, but the position repeats: the game is void.
    return applyMove(...) with phase = .finished, result = .noResult(.repetition)
```

Nước đi đó **vẫn hợp lệ** (khác với superko), nhưng ván kết thúc ngay với kết quả "vô hiệu".
Client phải hiển thị rõ: *"Ván vô hiệu do lặp thế cờ (ba kiếp)"* kèm nút chơi lại.

## 5. Nhường lượt và kết thúc ván

```
function applyMove(state, Pass) -> GameState:
    newState = state với:
        toPlay             = opponent(state.toPlay)
        moveNumber        += 1
        consecutivePasses += 1
        koPoint            = nil
        // NOTE: a pass does NOT add to positionHistory — the position is unchanged.

    if newState.consecutivePasses >= 2:
        newState.phase = .scoring
    return newState
```

Các cách ván kết thúc:

| Nguyên nhân | Mã | Ghi chú |
|-------------|-----|---------|
| Hai lần pass liên tiếp | `two_passes` | → giai đoạn đếm điểm |
| Xin thua | `resignation` | Kết thúc ngay, không đếm điểm |
| Hết giờ | `timeout` | Kết thúc ngay, không đếm điểm |
| Lặp thế (luật Nhật) | `repetition` | Vô hiệu, không có người thắng |
| Bỏ ván quá hạn | `abandonment` | Server xử sau 30 ngày không hoạt động |
| Hai bên đồng ý hòa | `mutual_draw` | P2 |

## 6. Giai đoạn đếm điểm

### 6.1 Quy trình

```
Playing ──(hai pass)──▶ Scoring ──(hai bên đồng ý)──▶ Finished
                            │
                            └──(một bên phản đối)──▶ Playing (từ thế trước nước pass đầu)
```

Trong giai đoạn `Scoring`:

1. Server chạy [thuật toán đề xuất quân chết](#64-đề-xuất-quân-chết-tự-động) và gửi kèm tỉ số dự kiến.
2. Mỗi bên có thể chạm vào một **đám quân** để bật/tắt trạng thái "chết". Thay đổi được
   đồng bộ realtime cho cả hai và tỉ số tính lại ngay.
3. Mỗi thay đổi sẽ **xóa trạng thái đồng ý** của cả hai bên (tránh việc một bên đồng ý rồi
   bên kia lén sửa).
4. Khi cả hai cùng ở trạng thái đồng ý → chốt kết quả.
5. Nếu một bên bấm "Chơi tiếp" → quay lại `Playing`, **lùi lần lượt từng nước pass cho tới
   khi bên phản đối được đi**.

> **Sửa lại khi cài đặt.** Bản đầu của mục này viết "quay lại thế cờ ngay trước nước pass đầu
> tiên, lượt đi thuộc về bên đã phản đối". Hai vế đó không phải lúc nào cũng cùng một thế cờ:
> nếu A pass trước rồi B pass, lùi cả hai nước sẽ tới lượt **A**, không phải B. Quy tắc lùi
> dần từng pass thỏa mãn cả hai vế trong mọi trường hợp:
>
> - **B phản đối** (người pass thứ hai) → lùi 1 pass → tới lượt B. ✓
> - **A phản đối** (người pass đầu) → lùi 1 pass, chưa tới lượt A → lùi tiếp → tới lượt A. ✓
>
> Cài đặt: `GameSession.resumePlay` trong `sente-server/internal/game/session.go`.

### 6.2 Đếm điểm — hệ luật Nhật Bản (đếm đất)

```
function scoreJapanese(board, deadStones, capturesDuringGame, komi) -> Score:
    // 1. Remove agreed-dead stones; they become prisoners of the opponent.
    working = board
    for stone in deadStones:
        working[stone] = Empty
        prisoners[opponent(colorOf(stone))] += 1

    // 2. Flood-fill every empty region; a region belongs to a colour only if every
    //    stone adjacent to the region has that colour. Regions touching both colours
    //    are dame (neutral) — this is also what makes seki score correctly.
    for region in emptyRegions(working):
        borders = colours of stones adjacent to region
        if borders == {Black}: territory[Black] += size(region)
        if borders == {White}: territory[White] += size(region)
        // both colours, or no colour (empty board) -> neutral, scores nothing

    black = territory[Black] + capturesDuringGame[Black] + prisoners[Black]
    white = territory[White] + capturesDuringGame[White] + prisoners[White] + komi
    return Score(black, white)
```

**Seki.** Quy tắc "vùng trống giáp cả hai màu thì không tính điểm" xử lý đúng seki thông
thường: mắt và dame trong seki đều giáp cả hai màu nên không tính cho ai. Trường hợp seki
có mắt riêng chỉ giáp một màu (hiếm, "seki có mắt") sẽ bị tính thành đất — đây là sai lệch
**đã biết và chấp nhận** với luật Nhật chính thống; ghi lại ở [§11](#11-sai-lệch-đã-biết).

### 6.3 Đếm điểm — hệ luật Trung Quốc (đếm diện tích)

```
function scoreChinese(board, deadStones, komi) -> Score:
    working = board
    for stone in deadStones: working[stone] = Empty      // prisoners are irrelevant here

    for colour in [Black, White]:
        area[colour] = count of stones of `colour` on `working`

    for region in emptyRegions(working):
        borders = colours of stones adjacent to region
        if borders == {Black}: area[Black] += size(region)
        if borders == {White}: area[White] += size(region)

    return Score(area[Black], area[White] + komi)
```

Không dùng tù binh. Với chấp quân, luật Trung Quốc chuẩn trừ của Đen `handicap` điểm; xem
[§8](#8-chấp-quân-handicap).

### 6.4 Đề xuất quân chết (tự động)

Con người là trọng tài cuối cùng — thuật toán chỉ để tiết kiệm thao tác. Hai tầng:

**Tầng 1 — Thuật toán Benson (chính xác, rẻ).** Tìm các đám quân **sống vô điều kiện**
(*pass-alive*): dù đối phương đi bao nhiêu nước liên tiếp cũng không giết được. Các đám này
**không bao giờ** được đề xuất là chết.

```
function bensonPassAliveChains(board, colour) -> Set<Chain>:
    X = tất cả đám quân màu `colour`
    R = tất cả vùng trống mà mọi giao điểm kề đều thuộc `colour`   // "colour-enclosed regions"
    lặp đến khi không đổi:
        loại khỏi R mọi vùng r có một giao điểm trống không kề đám nào còn trong X
                       (r không còn là "mắt sống" cho X)
        loại khỏi X mọi đám x có ít hơn 2 vùng trong R mà x kề với   // needs two eyes
    return X
```

**Tầng 2 — Ước lượng sở hữu bằng playout.** Với các đám không pass-alive, chạy `K = 300`
ván ngẫu nhiên nhẹ (random playout không đi vào mắt của chính mình) từ thế cờ cuối; đếm tần
suất mỗi giao điểm thuộc về mỗi màu. Đám nào có tỉ lệ sở hữu bởi màu của chính nó < 0.35 →
đề xuất là **chết**.

```
suggestDead(board):
    alive = bensonPassAliveChains(board, Black) ∪ bensonPassAliveChains(board, White)
    ownership = monteCarloOwnership(board, playouts: 300)
    return { chain | chain ∉ alive and ownership[chain.colour][chain] < 0.35 }
```

Ràng buộc: toàn bộ quá trình phải xong trong **< 300ms** trên bàn 19×19 ở server.
`K` là tham số cấu hình để đánh đổi độ chính xác/độ trễ.

> **Lộ trình.** Khi bổ sung KataGo ở v1.1, thay Tầng 2 bằng ownership map của mạng neural —
> chính xác hơn hẳn. Giao diện `suggestDead(board) -> Set<Chain>` giữ nguyên, nên đây là
> một thay thế cục bộ.

**Vị trí chạy.** Chỉ chạy ở **server**. Client nhận kết quả qua message `scoring_state`.
Lý do: (a) tránh hai bên thấy đề xuất khác nhau, (b) playout tốn CPU và pin.

## 7. Đồng hồ

Server là nguồn sự thật duy nhất về thời gian ([NFR-SEC3](01-requirements.md#54-bảo-mật--riêng-tư)).
Client chỉ hiển thị bộ đếm lùi dựa trên mốc server gửi.

### 7.1 Mô hình chung

```
ClockState {
  mainTimeRemainingMs:   [Black: Int, White: Int]
  periodsRemaining:      [Black: Int, White: Int]   // byo-yomi only
  periodTimeRemainingMs: [Black: Int, White: Int]   // byo-yomi only
  turnStartedAt:         Timestamp                  // server monotonic clock
  moveDeadline:          Timestamp                  // absolute; sent to clients
}
```

### 7.2 Trừ thời gian

```
function chargeClock(clock, player, receivedAt) -> ClockState | Timeout:
    elapsed = receivedAt - clock.turnStartedAt
    elapsed = max(0, elapsed - LAG_GRACE_MS)     // compensate for network latency

    switch timeControl:
      case .absolute:
        remaining = clock.mainTimeRemainingMs[player] - elapsed
        return remaining <= 0 ? .timeout : updated(remaining)

      case .fischer(increment, cap):
        remaining = clock.mainTimeRemainingMs[player] - elapsed
        if remaining <= 0 { return .timeout }
        return updated(min(remaining + increment, cap))

      case .byoyomi(periods, periodMs):
        remaining = clock.mainTimeRemainingMs[player] - elapsed
        if remaining >= 0 { return updated(mainTime: remaining) }   // still in main time

        // Main time exhausted: consume byo-yomi periods.
        overrun  = -remaining
        consumed = floor(overrun / periodMs)
        if consumed >= clock.periodsRemaining[player] { return .timeout }
        // Any move made inside a period resets that period in full.
        return updated(mainTime: 0,
                       periods: clock.periodsRemaining[player] - consumed,
                       periodTime: periodMs)

      case .correspondence(daysPerMove):
        return elapsed > daysPerMove * 86_400_000 ? .timeout : updated(...)
```

`LAG_GRACE_MS = 500`, cộng dồn tối đa 30 giây cho cả ván mỗi bên (chống lạm dụng bằng cách
cố tình làm chậm gói tin).

### 7.3 Hết giờ

Bên đang giữ ván (owner node, xem [04](04-architecture.md)) đặt một timer tới `moveDeadline`.
Khi timer nổ, server tự kết thúc ván với `timeout`. **Không** chờ client báo hết giờ.

Chống lệch đồng hồ ở client: mỗi message `clock_update` mang `serverTime`; client tính
`offset = serverTime - clientTime` và hiển thị `deadline - (now + offset)`. Ping/pong 20s
một lần để cập nhật offset.

### 7.4 Ván correspondence

Không giữ timer trong bộ nhớ. Một job quét chạy mỗi phút tìm ván có `move_deadline < now()`
và xử thua. Push nhắc trước deadline 25% thời gian còn lại (tối thiểu 1 giờ).

## 8. Chấp quân (handicap)

Đen đặt sẵn `H` quân trước khi ván bắt đầu, sau đó **Trắng đi trước**.

```
19×19 star points (dùng ký hiệu cột A–T bỏ chữ I):
  góc: D4, D16, Q4, Q16 · cạnh: D10, Q10, K4, K16 · tâm: K10
13×13: góc D4, D10, K4, K10 · tâm G7
 9×9:  góc C3, C7, G3, G7 · tâm E5
```

Thứ tự đặt quân chấp (chuẩn, dùng chung cho mọi cỡ bàn — thay tên điểm tương ứng):

| H | Các điểm (19×19) |
|---|------------------|
| 2 | Q16, D4 |
| 3 | Q16, D4, Q4 |
| 4 | Q16, D4, Q4, D16 |
| 5 | 4 góc + K10 |
| 6 | 4 góc + D10, Q10 |
| 7 | 4 góc + D10, Q10 + K10 |
| 8 | 4 góc + D10, Q10, K4, K16 |
| 9 | 8 điểm trên + K10 |

Quy tắc kèm theo:

- Komi mặc định về **0.5** khi `H > 0` (để không hòa). Người tạo ván vẫn chỉnh được.
- Với luật Trung Quốc, điểm của Đen bị trừ `H` khi đếm diện tích (bù cho `H` quân đặt sẵn).
- Thế cờ ban đầu (đã có quân chấp) được đưa vào `positionHistory` trước nước đầu tiên.
- Quân chấp **không** tính là nước đi: `moveNumber` bắt đầu từ 0, nước đầu của Trắng là nước 1.

## 9. SGF

Xuất/nhập theo SGF FF[4]. Ánh xạ trường:

| Trường SGF | Ý nghĩa | Ví dụ |
|-----------|---------|-------|
| `GM[1]FF[4]CA[UTF-8]` | Cờ vây, format 4 | bắt buộc |
| `SZ` | Cỡ bàn | `SZ[19]` |
| `KM` | Komi | `KM[6.5]` |
| `HA` + `AB` | Số quân chấp + vị trí đặt sẵn | `HA[4]AB[dd][pd][dp][pp]` |
| `RU` | Hệ luật | `RU[Japanese]` / `RU[Chinese]` |
| `PB` / `PW` | Tên người chơi | `PB[an]PW[binh]` |
| `RE` | Kết quả | `RE[W+3.5]`, `RE[B+R]`, `RE[W+T]`, `RE[Void]` |
| `TM` / `OT` | Thời gian chính / overtime | `TM[1200]OT[3x30 byo-yomi]` |
| `DT` | Ngày | `DT[2026-08-28]` |
| `AP` | App tạo file | `AP[Sente:1.0]` |
| `;B[dd]` / `;W[pd]` | Nước đi | tọa độ chữ cái a-s, `[]` hoặc `[tt]` = pass |
| `C[...]` | Bình luận / chat của nước đó | |

**Tọa độ SGF:** cột và hàng đều dùng `a..s` (0-based), gốc ở **góc trên-trái**. Lưu ý đây
là hệ khác với tọa độ hiển thị (A–T bỏ I, hàng đánh số từ dưới lên). Engine phải có hàm
chuyển đổi riêng và test cả hai chiều.

## 10. Hiệu năng và tối ưu

Yêu cầu: `< 50µs` cho một lần `isLegal + applyMove` trên bàn 19×19 ([NFR-P5](01-requirements.md#51-hiệu-năng)).

**Cài đặt cơ sở** (flood fill mỗi lần) đạt khoảng 2–5µs cho nước đi thường và ~30µs cho
nước bắt đám lớn — **đã đủ**. Ưu tiên sự đơn giản và tính đúng đắn.

**Chỉ tối ưu khi cần** cho Monte Carlo score estimator ([§6.4](#64-đề-xuất-quân-chết-tự-động)),
nơi cần hàng trăm nghìn nước/giây:

- Union-find với đếm khí tăng dần (`chainId[]`, `libertyCount[]`, `stoneCount[]`).
- Lưu tổng chỉ số khí (`libertySum`, `libertySumSquared`) để phát hiện khí về 0 mà không
  cần duyệt lại — kỹ thuật chuẩn của các engine cờ vây.
- Giữ đường code tối ưu **sau một interface chung** với đường code cơ sở, và chạy
  differential test: mọi thế cờ ngẫu nhiên phải cho kết quả giống hệt nhau ở cả hai.

## 11. Sai lệch đã biết

Ghi lại minh bạch để không ai "sửa" nhầm thành bug:

| # | Sai lệch | Lý do | Ảnh hưởng |
|---|----------|-------|-----------|
| D1 | Seki có mắt riêng bị tính là đất (luật Nhật) | Cài đặt đúng cần phân tích sống-chết đầy đủ | Cực hiếm; người chơi có thể sửa thủ công ở giai đoạn đếm điểm |
| D2 | Không hỗ trợ luật Nhật "bunnan" (phân xử sống chết chính thức) | Quá phức tạp cho v1.0 | Tranh chấp giải quyết bằng chơi tiếp ([§6.1](#61-quy-trình)) |
| D3 | Không hỗ trợ luật Ing / New Zealand (cho tự sát) | Ngoài phạm vi | — |
| D4 | Luật Trung Quốc dùng PSK, không dùng SSK (situational superko) | PSK là chuẩn phổ biến hơn ở phần mềm | Khác biệt chỉ xảy ra trong thế cờ nhân tạo |
| D5 | Không xử lý "nước đi trong seki bắt buộc" cuối ván (luật Nhật) | — | Ảnh hưởng ≤ 1 điểm trong thế hiếm |

## 12. Bộ test bắt buộc

Danh sách các thế cờ mà conformance vectors **phải** bao phủ (chi tiết ở [09](09-testing-strategy.md)):

- Bắt 1 quân, bắt nhiều đám cùng lúc bằng một nước.
- Tự sát 1 quân và tự sát cả đám → cấm.
- Nước "trông như tự sát nhưng bắt được quân" → hợp lệ.
- Ko cơ bản: bắt lại ngay → cấm; đánh dứ kiếp rồi bắt lại → hợp lệ.
- Superko: thế cờ lặp sau 6 nước → cấm ở luật Trung, vô hiệu ván ở luật Nhật.
- Ba kiếp (triple ko) → luật Nhật ra `no_result`.
- "Bent four in the corner", "two-headed dragon" — các thế kinh điển về sống chết.
- Seki cơ bản, seki có dame chung.
- Đếm điểm: cùng một thế cờ cho ra hai tỉ số khác nhau theo luật Nhật và luật Trung, và
  chênh lệch phải đúng bằng công thức lý thuyết.
- Bàn 9×9 lấp đầy hoàn toàn (mọi giao điểm có quân).
- Chấp quân 2–9 ở cả ba cỡ bàn: kiểm tra vị trí và lượt đi đầu tiên thuộc về Trắng.
