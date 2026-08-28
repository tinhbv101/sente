# 09 — Testing Strategy

> Yêu cầu chất lượng gốc ở [01 §5.3](01-requirements.md#53-chất-lượng--bảo-trì).
> Ràng buộc quan trọng nhất: **hai engine luật không được lệch nhau**
> ([ADR-002](03-solution-design.md#adr-002--nơi-đặt-engine-luật-cờ)).

## 1. Nguyên tắc

1. **Engine luật được test cạn kiệt; phần còn lại test đủ.** Một bug ở engine phá hỏng ván
   cờ vĩnh viễn; một bug ở màn hình Cài đặt thì không. Phân bổ công sức theo tỉ lệ đó.
2. **TDD cho engine và cho logic ván.** Viết vector/test trước, đỏ, rồi mới cài đặt.
3. **Mọi bug luật phát hiện được trở thành một conformance vector, trước khi sửa.** Đây là
   quy tắc bất khả xâm phạm — nó là cơ chế duy nhất giữ hai engine đồng bộ theo thời gian.
4. **Test phải chạy được offline và tất định.** Không phụ thuộc mạng, không phụ thuộc thời
   gian thực (tiêm `Clock`), không có `sleep`.

## 2. Kim tự tháp

```
                    ╱╲
                   ╱E2E╲              ~15 kịch bản · chạy trước release
                  ╱──────╲
                 ╱ UI/XC  ╲           ~25 test · chạy mỗi PR
                ╱──────────╲
               ╱ Integration╲         ~120 test · mỗi PR
              ╱──────────────╲
             ╱  Conformance   ╲       ~400 vectors · chạy ở CẢ HAI repo
            ╱──────────────────╲
           ╱       Unit         ╲     ~900 test
          ╱──────────────────────╲
```

Tầng **Conformance** không có trong kim tự tháp test thông thường. Nó là tầng riêng vì nó
chạy trên hai codebase khác nhau bằng hai ngôn ngữ khác nhau với cùng một bộ dữ liệu.

## 3. Conformance vectors — tầng quan trọng nhất

### 3.1 Định dạng

Sống trong repo `rules-spec/` (git submodule của cả `sente-server` và `sente-ios`):

```json
{
  "id": "capture-creates-liberty-so-not-suicide",
  "description": "Đen đặt vào điểm trông như tự sát nhưng bắt được đám Trắng nên hợp lệ",
  "rules": "japanese",
  "board_size": 9,
  "komi": 6.5,
  "handicap": 0,
  "setup": {
    "black": ["b1", "c2", "d1"],
    "white": ["c1"]
  },
  "to_play": "black",
  "move": "b2",
  "expect": {
    "legal": true,
    "captured": ["c1"],
    "captures_after": { "black": 1, "white": 0 }
  }
}
```

> **Board hash không nằm trong vector.** Không ai tính được Zobrist hash bằng tay, nên một
> vector chứa `board_hash` thực chất là chép lại output của engine — nó không kiểm tra gì cả.
> Thay vào đó, hash được khóa trong [`rules-spec/parity/positions.json`](../rules-spec/parity/positions.json):
> 36 thế cờ sinh tất định kèm hash, cả hai engine phải tái tạo đúng. Đây là **lưới chống hồi
> quy**, không phải nguồn sự thật — nó bắt việc một engine lặng lẽ đổi cách hash, đúng cái
> làm hỏng `resume` và phát hiện desync ([04 §4.2](04-architecture.md#42-kết-nối-lại-và-đồng-bộ-resume)).

Với vector về đếm điểm:

```json
{
  "id": "scoring-japanese-vs-chinese-same-position",
  "rules": "japanese",
  "board_size": 9,
  "komi": 6.5,
  "setup": { "black": ["..."], "white": ["..."] },
  "dead_stones": ["g7", "g8"],
  "captures_during_game": { "black": 3, "white": 5 },
  "expect": {
    "score": { "black": 24.0, "white": 27.5 },
    "detail": {
      "black": { "territory": 21, "captures": 3, "komi": 0 },
      "white": { "territory": 17, "captures": 5, "komi": 6.5 }
    }
  }
}
```

### 3.2 Cách chạy

```
sente-server: go test ./internal/rules -run TestConformance
sente-ios:    swift test --filter ConformanceTests
```

Cả hai đọc cùng thư mục `rules-spec/vectors/**/*.json`, chạy từng vector, so từng trường.
**CI của cả hai repo fail nếu bất kỳ vector nào không pass.** Không có cờ bỏ qua.

### 3.3 Độ bao phủ bắt buộc

Danh sách này lấy từ [02 §12](02-go-rules-spec.md#12-bộ-test-bắt-buộc) và là điều kiện nghiệm
thu của engine:

| Nhóm | Hiện có | Mục tiêu | Nội dung |
|------|--------:|---------:|----------|
| `legality/` | 9 | 60 | Ngoài bàn, đã có quân, sai lượt, sai phase |
| `capture/` | 5 | 80 | Bắt 1 quân, bắt nhiều đám, bắt ở góc/cạnh, đám lớn |
| `suicide/` | 4 | 40 | Tự sát 1 quân, tự sát cả đám, "trông như tự sát nhưng hợp lệ" |
| `ko/` | 5 | 50 | Ko cơ bản, đánh dứ kiếp, ko ở góc, ko liên hoàn |
| `superko/` | 2 | 30 | PSK cấm, hành vi khác nhau giữa luật Nhật và Trung, ba kiếp |
| `life_death/` | 3 | 60 | Bent four, two-headed dragon, mắt giả, seki các loại |
| `scoring/` | 4 | 70 | Nhật vs Trung trên cùng thế cờ, seki, quân chết, bàn lấp đầy |
| `handicap/` | 8 | 30 | H=2..9 × ba cỡ bàn, kiểm tra vị trí và lượt đi đầu |
| `sgf/` | 3 | 20 | Đọc/ghi hai chiều, ký tự lạ, pass, ván không có nước nào |
| **Tổng** | **43** | **440** | |

Khoảng cách này là cố ý: 43 vector hiện có là những trường hợp **kiểm chứng được bằng tay**
từ đặc tả. Phần còn lại phải đến từ hai nguồn mà chính tài liệu này đã nêu — corpus SGF và
lần chạy differential hàng đêm — và cả hai đều chưa dựng. Chi tiết các lỗ hổng đã biết
(vòng lặp thế dài, ba kiếp) ở [`rules-spec/SCHEMA.md`](../rules-spec/SCHEMA.md).

Vector được sinh một phần từ **corpus SGF thật**: 1.000 ván chuyên nghiệp tải từ nguồn công
khai, replay qua engine tham chiếu (GNU Go hoặc Sabaki) để lấy kết quả kỳ vọng, rồi trích các
thế cờ thú vị.

### 3.4 Kiểm tra chéo với engine tham chiếu

Ngoài vectors tĩnh, chạy **differential testing** trong CI hàng đêm:

```
Sinh 10.000 ván ngẫu nhiên (nước đi hợp lệ chọn ngẫu nhiên, tối đa 400 nước)
  Với mỗi nước:
    so sánh isLegal() giữa Go engine, Swift engine, và GNU Go
    so sánh board hash sau mỗi nước giữa Go và Swift
  Bất kỳ khác biệt nào ⇒ fail + lưu ván đó thành vector mới
```

Đây là mạng lưới bắt được những gì vectors thủ công bỏ sót. Chạy hàng đêm vì nó tốn ~10 phút.

## 4. Backend

### 4.1 Unit

| Gói | Trọng tâm | Coverage mục tiêu |
|-----|-----------|-------------------|
| `internal/rules` | Toàn bộ luật + conformance | **95% dòng / 90% nhánh** |
| `internal/game/clock` | Từng thể thức thời gian, biên (hết đúng lúc, byo-yomi kỳ cuối) | 95% |
| `internal/game/actor` | Máy trạng thái, xử lý lệnh sai phase | 90% |
| `internal/auth` | JWT, rotation, phát hiện reuse | 95% |
| Còn lại | | 80% |

**Đồng hồ phải tiêm được.** Không gọi `time.Now()` trực tiếp ở bất kỳ đâu trong logic:

```go
// Clock is injected so tests can advance time deterministically instead of sleeping.
type Clock interface {
    Now() time.Time
    NewTimer(d time.Duration) *time.Timer
}
```

Test byo-yomi kỳ cuối, hết giờ đúng mili giây, bù trễ có trần — không thứ nào trong số đó test
được nếu phải chờ thời gian thật.

### 4.2 Property-based

Dùng `testing/quick` hoặc `pgregory.net/rapid`:

| Bất biến | Phát biểu |
|----------|-----------|
| Replay tất định | Replay toàn bộ `moves` cho ra đúng `board_hash` đã lưu ở mỗi nước |
| Bất biến tù binh | Tổng quân trên bàn + tù binh = tổng quân đã đặt |
| Đối xứng luật | Xoay/lật bàn cờ không đổi tính hợp lệ của nước đi tương ứng |
| Không mất khí | Mọi đám quân trên bàn sau một nước hợp lệ đều có ≥ 1 khí |
| Đếm điểm cộng dồn | Với luật Trung, `black + white = boardSize² + komi` khi mọi giao điểm thuộc về ai đó |
| Idempotency | Áp dụng cùng `client_move_id` hai lần cho cùng kết quả |

### 4.3 Integration

Dùng `testcontainers-go` với PostgreSQL và Redis thật (không mock DB — mock DB che giấu đúng
những bug mà integration test cần bắt).

| Kịch bản | Kiểm tra |
|----------|----------|
| Ván trọn vẹn qua WebSocket | Từ tạo lời mời tới `game_over`, kiểm tra dữ liệu DB cuối cùng |
| Hai client đồng thời đi | Chỉ một nước được chấp nhận; bên kia nhận `not_your_turn` |
| Gửi lại `client_move_id` | Trả về kết quả cũ, `moves` chỉ có một dòng |
| Đứt kết nối giữa ván + `resume` | `sync_delta` đúng, nước trong outbox áp dụng đúng một lần |
| Hết giờ | Timer server kích hoạt, cả hai client nhận `game_over` |
| Đếm điểm có tranh chấp | `scoring_resume` đưa ván về đúng thế trước nước pass đầu |
| Ván correspondence | Đi qua REST, deadline đúng, job sweeper xử hết giờ đúng |
| Xóa tài khoản | Dữ liệu bị xóa/ẩn danh đúng theo [05 §14](05-data-model.md#14-vòng-đời-dữ-liệu-và-quyền-riêng-tư) |
| IDOR | Mỗi endpoint có `{id}`: user B truy cập tài nguyên của A → 403/404 |

### 4.4 Chaos test — bắt buộc trước release

Đây là phần rủi ro nhất của kiến trúc ([ADR-005](03-solution-design.md#adr-005--sở-hữu-ván-single-owner-actor--định-tuyến-qua-redis)),
nên nó cần test riêng chứ không chỉ integration test.

| Kịch bản | Kỳ vọng |
|----------|---------|
| Kill node sở hữu ván giữa lúc đang đi | Node khác nhận lease, replay từ DB, không mất nước đi, có `clock_adjusted` |
| Kill node **sau** khi commit DB nhưng **trước** khi publish | Client reconnect nhận được nước đi qua `resume` |
| Redis mất trong 30 giây | Ván trên cùng node tiếp tục; ván xuyên node khôi phục sau khi Redis trở lại |
| PostgreSQL failover | Nước đi bị hoãn ≤ 60s rồi tiếp tục, hoặc bị từ chối rõ ràng — không bao giờ "mất" |
| Hai node cùng giành lease | Đúng một node thắng (`SET NX`); node thua không tạo actor |
| Rolling deploy dưới tải | 100 ván đang chơi, không ván nào hỏng, gián đoạn < 1s mỗi ván |
| Đồng hồ server bị nhảy | Dùng monotonic clock nên không ảnh hưởng; test bằng cách chỉnh wall clock |

Chạy bằng `toxiproxy` (mô phỏng độ trễ, mất gói, đứt kết nối) + script kill container.

### 4.5 Load test

Công cụ: `k6` với extension WebSocket, hoặc một client Go tự viết (kiểm soát tốt hơn).

| Kịch bản | Mục tiêu | Tiêu chí đạt |
|----------|----------|--------------|
| Baseline | 1.000 ván đồng thời, 1 nước/20s | p95 `move_apply_duration` < 30ms |
| Mục tiêu | 10.000 ván đồng thời | p95 < 30ms, RAM < 2GB/node, 0 lỗi |
| Đột biến | 0 → 5.000 ván trong 60 giây | Không lỗi, đồng hồ vẫn chính xác |
| Kết nối dồn | 20.000 reconnect trong 30 giây (mô phỏng sự cố diện rộng) | Backoff + jitter phân tán tải, không sập |
| Ván dài | 500 ván chạy liên tục 2 giờ | Không rò rỉ bộ nhớ (RSS ổn định) |
| Đếm điểm | 100 ván cùng vào phase scoring | Monte Carlo không làm nghẽn; p95 < 300ms |

Chạy trên staging trước mỗi release lớn; kết quả lưu lại để so sánh giữa các phiên bản.

## 5. iOS

Chi tiết ở [07 §12](07-ios-app-design.md#12-kiểm-thử-phía-client). Bổ sung ở đây:

### 5.1 Snapshot test cho bàn cờ

Ma trận: `{9, 13, 19} × {light, dark} × {default, XXL Dynamic Type} × {bình thường, mù màu}`
với 5 thế cờ chuẩn = 180 ảnh. Chạy trên **một simulator cố định** (iPhone 15, iOS 17.5) để
tránh khác biệt render giữa các máy.

Đây là cách rẻ nhất để bắt lỗi hồi quy về layout bàn cờ — thứ mà unit test không thấy được.

### 5.2 XCUITest — 5 luồng bắt buộc

1. Onboarding → tạo lời mời → sao chép link.
2. Mở deep link → xem trước → chấp nhận → vào ván.
3. Chơi trọn ván 9×9 với server giả → hai pass → đếm điểm → kết quả.
4. Kill app giữa ván → mở lại → ván khôi phục đúng thế cờ và đồng hồ.
5. Bật chế độ máy bay → đặt quân → tắt máy bay → nước đi được gửi đúng một lần.

Luồng 4 và 5 dùng launch argument để trỏ app vào server giả cục bộ, không phụ thuộc mạng.

### 5.3 Kiểm thử thủ công trước release

Checklist chạy trên thiết bị thật, không phải simulator:

- [ ] Chuyển WiFi ↔ 4G giữa ván (không phải bật/tắt máy bay — chuyển mạng là ca khác).
- [ ] Cuộc gọi đến giữa ván.
- [ ] Face ID / màn hình khóa giữa ván, mở lại sau 5 phút.
- [ ] Chế độ nguồn thấp bật.
- [ ] VoiceOver: chơi trọn một ván 9×9 chỉ bằng VoiceOver.
- [ ] Dynamic Type ở cỡ lớn nhất trên iPhone SE.
- [ ] Xoay ngang màn hình (kể cả khi đang kéo quân).
- [ ] Push đến khi app đã bị kill → chạm → mở đúng ván.
- [ ] Chơi 30 phút liên tục, kiểm tra nhiệt độ máy và mức pin tiêu thụ ([NFR-P7](01-requirements.md#51-hiệu-năng)).

## 6. End-to-end

Hai thiết bị thật (hoặc một thiết bị + một simulator), server staging thật:

| # | Kịch bản | Tiêu chí |
|---|----------|----------|
| E1 | Ván 19×19 trọn vẹn với byo-yomi, kết thúc bằng đếm điểm | Kết quả đúng ở cả hai máy |
| E2 | Ván kết thúc bằng xin thua | Cả hai thấy kết quả trong < 1s |
| E3 | Ván kết thúc bằng hết giờ ở kỳ byo-yomi cuối | Cả hai thấy cùng lúc, không ai thấy "còn giờ" |
| E4 | Một máy mất mạng 60 giây giữa ván | Khôi phục, không mất nước đi, có bù giờ |
| E5 | Ván correspondence qua 3 ngày (giả lập bằng chỉnh deadline) | Push đúng, deadline đúng |
| E6 | Tranh chấp quân chết → chơi tiếp → kết thúc lại | Thế cờ quay lại đúng chỗ |
| E7 | Deploy backend giữa lúc đang có ván | Ván không hỏng, gián đoạn thoáng qua |
| E8 | Chấp 9 quân trên 19×19, luật Trung | Vị trí đúng, Trắng đi trước, điểm trừ đúng |

E1, E3, E4, E6 là **điều kiện nghiệm thu MVP** ([01 §10](01-requirements.md#10-tiêu-chí-nghiệm-thu-mvp)).

## 7. CI/CD

### 7.1 Pipeline server

```
PR:
  1. lint (golangci-lint) + gofmt check
  2. go build
  3. go test ./... -race -cover          ← gồm conformance vectors
  4. kiểm tra ngưỡng coverage (fail nếu rules < 95% hoặc tổng < 80%)
  5. govulncheck
  6. gitleaks
  7. integration test (testcontainers)
  8. build image + scan (trivy)

Merge vào main:
  9.  deploy staging tự động
  10. smoke test trên staging
  11. chaos test (nightly, không chặn merge)
  12. load test (nightly)

Release:
  13. phê duyệt thủ công → deploy prod (rolling, drain đúng quy trình 04 §5.2)
```

### 7.2 Pipeline iOS

```
PR:
  1. swiftlint
  2. swift build (tất cả package)
  3. swift test (GoKit + conformance vectors + các package khác)
  4. kiểm tra ngưỡng coverage
  5. xcodebuild test (unit + snapshot + XCUITest trên simulator cố định)
  6. build archive (không ký) để bắt lỗi build release sớm

Merge vào main:
  7. build + ký + upload TestFlight (internal)

Release:
  8. checklist thủ công (§5.3) → promote lên TestFlight external → App Store
```

### 7.3 Cổng chất lượng

| Cổng | Ngưỡng | Chặn merge? |
|------|--------|-------------|
| Conformance vectors | 100% pass | **Có** |
| Coverage `rules`/`GoKit` | ≥ 95% dòng, ≥ 90% nhánh | **Có** |
| Coverage tổng | ≥ 80% | **Có** |

> **Ghi chú khi cài đặt.** Bản đầu của `scripts/coverage_gate.py` áp ngưỡng 95% của engine
> lên **toàn bộ** module Go — chặt hơn NFR-Q2 và âm thầm đẩy người viết vào chỗ đẻ ra test
> mỏng cho các nhánh lỗi không thể xảy ra, chỉ để nhích một con số. Cổng phải mã hóa đúng
> yêu cầu, không phải một con số ai đó chọn. Nay tách hai ngưỡng theo đúng NFR-Q1/Q2.
| `govulncheck` / Dependabot cao | 0 vấn đề | **Có** |
| Integration test | 100% pass | **Có** |
| Snapshot test | 100% pass (cập nhật có review) | **Có** |
| Chaos test | 100% pass | Không (nightly) — nhưng chặn **release** |
| Load test | Đạt ngưỡng NFR | Không — nhưng chặn **release lớn** |

## 8. Quản lý test không ổn định (flaky)

- Test flaky bị **quarantine trong 24 giờ** (đánh dấu skip + tạo issue), không được để nó
  làm hỏng tín hiệu của CI.
- Quá 24 giờ chưa sửa → xóa test và tạo issue ưu tiên cao. Một test bị skip lâu ngày còn tệ
  hơn không có test, vì nó tạo cảm giác an toàn giả.
- Nguồn flaky phổ biến đã biết trong dự án này: XCUITest chờ animation, integration test phụ
  thuộc thời gian thật, test WebSocket không chờ đúng sự kiện. Cả ba đều có giải pháp tất
  định — sửa nguyên nhân, không thêm `sleep`.

## 9. Ma trận: yêu cầu ↔ test

| Yêu cầu | Được bảo vệ bởi |
|---------|-----------------|
| [G4](01-requirements.md#21-mục-tiêu-sản-phẩm) đúng luật tuyệt đối | Conformance vectors + differential testing + SGF corpus |
| [G2](01-requirements.md#21-mục-tiêu-sản-phẩm) ván không hỏng | Chaos test + integration test + E1–E8 |
| [G3](01-requirements.md#21-mục-tiêu-sản-phẩm) chơi khi mạng chập chờn | E4, XCUITest #5, integration "resume" |
| [NFR-P1/P2](01-requirements.md#51-hiệu-năng) độ trễ | Load test baseline + mục tiêu |
| [NFR-S1/S2](01-requirements.md#52-khả-năng-chịu-tải--sẵn-sàng) chịu tải | Load test |
| [NFR-S4](01-requirements.md#52-khả-năng-chịu-tải--sẵn-sàng) mất node | Chaos test |
| [NFR-Q3](01-requirements.md#53-chất-lượng--bảo-trì) hai engine không lệch | Conformance vectors chạy ở cả hai repo |
| [NFR-SEC*](01-requirements.md#54-bảo-mật--riêng-tư) | Integration IDOR test, rate limit test, checklist [08 §11](08-security-fairplay.md#11-checklist-trước-khi-phát-hành) |
| [NFR-A11Y*](01-requirements.md#55-khả-năng-tiếp-cận--bản-địa-hóa) | Snapshot test + checklist VoiceOver thủ công |
