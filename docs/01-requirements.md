# 01 — Requirements (PRD)

> Trạng thái: Draft v1 · Phạm vi: MVP → v1.0 · Liên quan: [03-solution-design.md](03-solution-design.md), [10-roadmap.md](10-roadmap.md)

## 1. Bối cảnh & vấn đề

Người chơi cờ vây ở Việt Nam hiện chủ yếu dùng OGS (web, không tối ưu mobile), Fox Weiqi
hoặc Tygem (UI cũ, tiếng Trung/Hàn, đăng ký khó). Nhu cầu cụ thể của người dùng mục tiêu:
**muốn mở app lên, gửi cho bạn một đường link/mã, và chơi ngay một ván**, không cần tạo
tài khoản phức tạp, không cần tìm hiểu hệ thống xếp hạng.

## 2. Mục tiêu

### 2.1 Mục tiêu sản phẩm

| # | Mục tiêu | Chỉ số đo (v1.0) |
|---|----------|------------------|
| G1 | Mời bạn và bắt đầu ván trong dưới 30 giây | p50 thời gian từ mở app → nước đi đầu tiên < 30s |
| G2 | Ván cờ không bao giờ "hỏng" vì lỗi kỹ thuật | Tỉ lệ ván kết thúc bất thường (crash/desync/lỗi luật) < 0.1% |
| G3 | Chơi được cả khi mạng chập chờn | Ván khôi phục thành công sau mất kết nối ≥ 99% trong vòng 60s |
| G4 | Đúng luật tuyệt đối | 0 lỗi luật xác nhận được trong 10.000 ván đầu tiên |
| G5 | Giữ chân người chơi | D7 retention ≥ 25% với người đã chơi ≥ 1 ván trọn vẹn |

### 2.2 Không phải mục tiêu (Non-goals)

- Không làm nền tảng thi đấu xếp hạng chuyên nghiệp (không có giải đấu, không có huy hiệu dan chính thức).
- Không có yếu tố cá cược, tiền thật, hay vật phẩm quy đổi.
- Không xây engine AI riêng. AI đối thủ (nếu có, ở v1.1) sẽ dùng KataGo chạy server-side.
- Không hỗ trợ Android / web ở v1.0 (nhưng backend phải trung lập với client — xem [04](04-architecture.md)).
- Không làm dạy học/giải bài tập tsumego ở v1.0.

## 3. Người dùng mục tiêu

### Persona A — "Người mới, chơi vì bạn rủ" (ưu tiên cao nhất)

Biết luật cơ bản, chưa nắm ko/seki/cách đếm điểm. Chơi bàn 9×9 hoặc 13×13. **Nhu cầu:**
app phải chặn nước đi sai luật và giải thích *tại sao* sai; kết thúc ván phải tự đề xuất
quân chết và điểm số, không bắt người chơi tự đếm.

### Persona B — "Người chơi đều, 5k–1d"

Đã chơi trên OGS/Fox. Chơi 19×19, quan tâm byo-yomi, komi đúng chuẩn, superko, xuất SGF
để phân tích. **Nhu cầu:** app không được sai luật, đồng hồ phải chính xác, phải có lịch sử
ván và replay.

### Persona C — "Cặp bạn chơi async"

Hai người ở hai múi giờ / bận. Chơi correspondence 1–3 ngày một nước. **Nhu cầu:** push
notification khi đến lượt, ván không bị hủy khi app bị kill, danh sách "đang chờ bạn đi".

## 4. Phạm vi chức năng

Ký hiệu ưu tiên: **P0** = bắt buộc cho MVP · **P1** = cần cho v1.0 · **P2** = sau v1.0.

### 4.1 Tài khoản & danh tính

| ID | Yêu cầu | Ưu tiên |
|----|---------|---------|
| FR-A1 | Người dùng chơi được ngay với tài khoản khách (guest) gắn với thiết bị, có tên hiển thị tự sinh và đổi được | P0 |
| FR-A2 | Đăng nhập bằng Sign in with Apple; nâng cấp tài khoản khách lên tài khoản thật mà không mất lịch sử ván | P0 |
| FR-A3 | Mỗi người có một **mã bạn bè** (friend code) 8 ký tự để người khác tìm và kết bạn | P1 |
| FR-A4 | Danh sách bạn bè: gửi/chấp nhận/từ chối lời mời, xem trạng thái online | P1 |
| FR-A5 | Xóa tài khoản và toàn bộ dữ liệu cá nhân trong app (bắt buộc theo App Store Guideline 5.1.1(v)) | P0 |
| FR-A6 | Đăng nhập bằng email/OTP cho người không dùng Apple ID | P2 |

### 4.2 Tạo và ghép ván

| ID | Yêu cầu | Ưu tiên |
|----|---------|---------|
| FR-M1 | Tạo **lời mời (challenge)** với cấu hình: cỡ bàn (9/13/19), hệ luật (Nhật/Trung), komi, chấp quân, thể thức thời gian, màu quân (đen/trắng/ngẫu nhiên). Thời gian chính mỗi bên do người dùng chọn, **trần theo cỡ bàn: 3 giờ (9×9), 9 giờ (13×13), 24 giờ (19×19)**; ván thư tín tính theo ngày/nước, không áp trần này | P0 |
| FR-M2 | Chia sẻ lời mời qua Universal Link (`https://sente.app/j/<code>`) bằng iOS Share Sheet — mở app nếu đã cài, mở App Store nếu chưa | P0 |
| FR-M3 | Người nhận xem trước cấu hình ván và Chấp nhận / Từ chối. Lời mời hết hạn sau 7 ngày | P0 |
| FR-M4 | Mời trực tiếp một người trong danh sách bạn bè (không cần link) | P1 |
| FR-M5 | Chơi trên cùng một máy (pass-and-play), không cần mạng | P1 — **đã làm 2026-08-30** (Home → "Trên máy này") |
| FR-M6 | Ghép ngẫu nhiên với người lạ theo hạng, có bộ lọc cỡ bàn và thể thức thời gian | P2 |
| FR-M7 | Xem người khác chơi (spectate) qua link ván công khai | P2 |

### 4.3 Chơi ván (core)

| ID | Yêu cầu | Ưu tiên |
|----|---------|---------|
| FR-G1 | Đặt quân đúng luật: luân phiên, cấm chồng quân, bắt quân khi hết khí, cấm tự sát, luật ko | P0 |
| FR-G2 | Áp dụng **positional superko** cho hệ luật Trung Quốc; **basic ko** cho hệ luật Nhật (chi tiết [02](02-go-rules-spec.md)) | P0 |
| FR-G3 | Nước đi sai luật bị chặn ở client kèm giải thích ngắn ("Nước này tự sát", "Cấm bởi luật ko"); server vẫn xác thực lại độc lập | P0 |
| FR-G4 | Đặt quân theo cơ chế **kéo–thả với con trỏ lệch lên trên và xác nhận nhả tay**, tránh đặt nhầm trên màn hình cảm ứng | P0 |
| FR-G5 | Nhường lượt (pass) và xin thua (resign) | P0 |
| FR-G6 | Hai lần pass liên tiếp → chuyển sang **giai đoạn đánh dấu quân chết** | P0 |
| FR-G7 | Trong giai đoạn đánh dấu: hệ thống tự đề xuất quân chết; mỗi bên chỉnh sửa và bấm Đồng ý. Hai bên đồng ý → kết thúc, tính điểm | P0 |
| FR-G8 | Nếu một bên không đồng ý kết quả → quay lại chơi tiếp từ vị trí trước khi pass | P0 |
| FR-G9 | Đồng hồ: **Absolute**, **Byo-yomi** (Nhật), **Fischer increment**, **Correspondence** (theo ngày). Hết giờ → thua | P0 |
| FR-G10 | Xin hoãn nước (undo request); đối thủ chấp nhận thì lùi 1 nước. Tối đa 3 lần/ván | P1 |
| FR-G11 | Chat trong ván (text), có nút tắt chat | P1 |
| FR-G12 | Hiển thị: số tù binh mỗi bên, số nước, nước đi cuối cùng được đánh dấu, các nước gần đây đánh số (tùy chọn) | P0 |
| FR-G13 | Bảng tọa độ (A-T, 1-19) bật/tắt được | P1 |
| FR-G14 | Ván bị bỏ dở tự động hủy sau 30 ngày không hoạt động, ghi nhận là "abandoned" | P1 |

### 4.4 Sau ván đấu

| ID | Yêu cầu | Ưu tiên |
|----|---------|---------|
| FR-R1 | Màn hình kết quả: người thắng, tỉ số chi tiết (đất + tù binh + komi), lý do kết thúc | P0 |
| FR-R2 | Lịch sử ván đấu, lọc theo đối thủ / cỡ bàn / kết quả | P0 |
| FR-R3 | Replay ván: tua tới/lui từng nước, nhảy tới nước bất kỳ, xem biến hóa thử (variation) mà không ảnh hưởng ván gốc | P1 |
| FR-R4 | Xuất SGF (chuẩn FF[4]) qua Share Sheet; nhập SGF để xem lại | P1 |
| FR-R5 | Hệ thống hạng Glicko-2 quy đổi ra kyu/dan, chỉ tính cho ván xếp hạng | P2 |

### 4.5 Thông báo & vòng đời app

| ID | Yêu cầu | Ưu tiên |
|----|---------|---------|
| FR-N1 | Push (APNs) khi: đối thủ chấp nhận lời mời, đến lượt bạn (ván correspondence), sắp hết giờ (còn 10% thời gian), ván kết thúc | P0 |
| FR-N2 | Người dùng bật/tắt từng loại thông báo | P1 |
| FR-N3 | Chạm vào push → mở thẳng ván tương ứng (deep link) | P0 |
| FR-N4 | Ván realtime tiếp tục chạy đồng hồ khi app vào nền; app quay lại foreground phải đồng bộ lại trạng thái trong < 1s | P0 |
| FR-N5 | Badge số ván đang chờ bạn đi | P1 |

### 4.6 An toàn & kiểm duyệt

| ID | Yêu cầu | Ưu tiên |
|----|---------|---------|
| FR-S1 | Báo cáo người chơi (bỏ ván, quấy rối, nghi dùng AI) và chặn người chơi | P0 (bắt buộc theo Guideline 1.2 nếu có chat/UGC) |
| FR-S2 | Lọc từ ngữ tục tĩu ở tên hiển thị và chat | P1 |
| FR-S3 | Ghi nhận và cảnh cáo hành vi bỏ ván thường xuyên (escape) | P2 |
| FR-S4 | Phát hiện nghi vấn dùng AI hỗ trợ (đối chiếu nước đi với engine, phân tích thời gian suy nghĩ) | P2 |

## 5. Yêu cầu phi chức năng

### 5.1 Hiệu năng

| ID | Yêu cầu | Ngưỡng |
|----|---------|--------|
| NFR-P1 | Độ trễ nước đi đầu-cuối (client A nhả tay → client B thấy quân) | p50 < 200ms, p95 < 500ms trên 4G tại VN |
| NFR-P2 | Thời gian server xử lý một nước đi (nhận → phát broadcast) | p95 < 30ms |
| NFR-P3 | Phản hồi cục bộ khi đặt quân (chưa chờ server) | < 16ms (1 frame ở 60Hz) |
| NFR-P4 | Cold start đến màn hình chính | p95 < 1.5s trên iPhone 12 |
| NFR-P5 | Xác thực luật một nước đi trên bàn 19×19 | < 50µs (cả Swift lẫn Go) |
| NFR-P6 | Bộ nhớ app khi đang chơi | < 120 MB |
| NFR-P7 | Tiêu thụ pin khi ván realtime chạy nền 30 phút | < 3% pin iPhone 13 |

### 5.2 Khả năng chịu tải & sẵn sàng

| ID | Yêu cầu | Ngưỡng (v1.0) |
|----|---------|---------------|
| NFR-S1 | Ván realtime đồng thời | 10.000 |
| NFR-S2 | Kết nối WebSocket đồng thời | 30.000 (gồm cả spectator, nhiều thiết bị) |
| NFR-S3 | Uptime dịch vụ chơi ván | 99.9% / tháng |
| NFR-S4 | Mất một node realtime không được làm hỏng ván đang chơi | Ván khôi phục trong ≤ 10s, không mất nước đi đã được xác nhận |
| NFR-S5 | RPO / RTO cho dữ liệu ván | RPO ≤ 1 phút, RTO ≤ 30 phút |

### 5.3 Chất lượng & bảo trì

| ID | Yêu cầu |
|----|---------|
| NFR-Q1 | Engine luật: coverage dòng ≥ 95%, coverage nhánh ≥ 90%, có property-based test |
| NFR-Q2 | Toàn bộ codebase: coverage ≥ 80% (theo quy ước team) |
| NFR-Q3 | Hai bản engine (Swift/Go) phải pass cùng một bộ conformance vectors trong CI |
| NFR-Q4 | Mọi thay đổi protocol phải tương thích ngược ít nhất 2 phiên bản app |
| NFR-Q5 | Log có `trace_id` xuyên suốt client → gateway → DB |

### 5.4 Bảo mật & riêng tư

Chi tiết ở [08-security-fairplay.md](08-security-fairplay.md). Tóm tắt yêu cầu:

- NFR-SEC1: Toàn bộ giao tiếp qua TLS 1.3. WebSocket dùng `wss://`.
- NFR-SEC2: Access token JWT hạn ngắn (15 phút) + refresh token quay vòng, lưu trong Keychain.
- NFR-SEC3: Server không bao giờ tin client về tính hợp lệ của nước đi hay thời gian còn lại.
- NFR-SEC4: Rate limit theo user và theo IP trên mọi endpoint.
- NFR-SEC5: Không thu thập dữ liệu ngoài mức cần thiết; khai báo đúng App Privacy Nutrition Label.

### 5.5 Khả năng tiếp cận & bản địa hóa

| ID | Yêu cầu |
|----|---------|
| NFR-A11Y1 | VoiceOver đọc được bàn cờ: mỗi giao điểm có label dạng "D4, quân đen" / "Q16, trống"; đặt quân được bằng VoiceOver |
| NFR-A11Y2 | Hỗ trợ Dynamic Type đến cỡ XXL cho toàn bộ text ngoài bàn cờ |
| NFR-A11Y3 | Có tùy chọn đánh dấu quân bằng ký hiệu (△/○) cho người mù màu, không chỉ dựa vào màu |
| NFR-A11Y4 | Haptic feedback khi đặt quân và khi bắt quân, tắt được |
| NFR-I18N1 | Ngôn ngữ: Tiếng Việt (mặc định) và English ở v1.0 |
| NFR-I18N2 | Thời gian hiển thị theo múi giờ thiết bị; ván correspondence hiển thị deadline tuyệt đối |

### 5.6 Nền tảng

| ID | Yêu cầu |
|----|---------|
| NFR-PL1 | iOS 17.0+ (bao phủ ~92% thiết bị đang hoạt động tại thời điểm phát hành) |
| NFR-PL2 | iPhone bắt buộc; iPad hỗ trợ ở chế độ tương thích ở MVP, layout riêng ở v1.0 |
| NFR-PL3 | Hỗ trợ Dark Mode |
| NFR-PL4 | Kích thước app khi tải về < 40 MB |

## 6. Trải nghiệm chính (user journeys)

### J1 — Rủ bạn chơi ván đầu tiên (P0)

```
An mở app (lần đầu, chưa có tài khoản)
  → App tạo tài khoản khách "an-cao-thu-2481", vào thẳng màn hình chính
  → Bấm "Mời bạn chơi"
  → Chọn: bàn 9×9 · luật Nhật · komi 6.5 · 10 phút + byo-yomi 3×30s · màu ngẫu nhiên
  → Bấm "Tạo lời mời" → Share Sheet → gửi link qua Zalo
Bình nhận link, chạm vào
  → Chưa cài app → App Store → cài → mở → app khôi phục link mời (deferred deep link)
  → Xem trước cấu hình ván, thấy "An mời bạn chơi" → bấm "Chấp nhận"
  → Server bốc màu ngẫu nhiên, tạo ván, push cho An
Hai người vào ván, An cầm Đen đi trước
```

**Yêu cầu ẩn rút ra:** cần deferred deep link (link vẫn hoạt động sau khi cài app mới),
cần tài khoản khách không ma sát, cần màn hình xem trước lời mời.

### J2 — Kết thúc ván và đếm điểm (P0)

```
Hai bên đều pass
  → App chuyển sang giai đoạn "Đánh dấu quân chết"
  → Server chạy thuật toán đề xuất quân chết, gửi kèm tỉ số dự kiến
  → Cả hai thấy quân chết mờ đi, vùng đất được tô màu, tỉ số hiện ở trên
  → An chạm vào một đám quân mình cho là còn sống → đám đó bỏ đánh dấu, tỉ số cập nhật
     tức thì cho cả hai (đây là thao tác đồng bộ realtime)
  → Cả hai bấm "Đồng ý kết quả"
  → Ván kết thúc: "Trắng thắng 3.5 điểm"
```

**Trường hợp tranh chấp:** nếu Bình bấm "Không đồng ý", ván quay lại trạng thái ngay
trước nước pass đầu tiên và hai bên chơi tiếp để phân định.

### J3 — Mất mạng giữa ván realtime (P0)

```
An đang trong ván, tàu điện chui hầm, mất sóng 40 giây
  → App phát hiện WS đứt → hiện banner "Đang kết nối lại…", đồng hồ vẫn chạy theo
     deadline đã biết (client tự đếm lùi từ mốc server gửi lần cuối)
  → An vẫn đặt được quân → nước đi vào hàng đợi gửi, kèm idempotency key
  → Có sóng lại → WS reconnect → gửi `resume` với `last_known_move_no`
  → Server trả về các nước đi còn thiếu + trạng thái đồng hồ chính xác
  → App gửi nước đi đang chờ; nếu server từ chối (đã quá giờ / sai lượt), app rollback
     nước đi cục bộ và hiện lý do
```

### J4 — Ván correspondence (P1)

```
An đi một nước lúc 22h, đóng app
  → Server đặt deadline cho Bình: +2 ngày
  → Push cho Bình "Đến lượt bạn — ván với An"
Bình mở app sáng hôm sau từ push
  → Vào thẳng ván, đi một nước, đóng app
  → Còn 18 giờ trước deadline → server gửi push nhắc trước 6 giờ
```

## 7. Cấu hình ván được hỗ trợ

| Tham số | Giá trị cho phép | Mặc định |
|---------|------------------|----------|
| Cỡ bàn | 9×9, 13×13, 19×19 | 19×19 (9×9 nếu người dùng tự nhận là mới) |
| Hệ luật | Nhật Bản (đếm đất), Trung Quốc (đếm diện tích) | Nhật Bản |
| Komi | 0 – 10.5, bước 0.5 | 6.5 (Nhật), 7.5 (Trung) |
| Chấp quân | 0, 2 – 9 quân | 0 |
| Thể thức | Absolute · Byo-yomi · Fischer · Correspondence | Byo-yomi |
| Thời gian chính | 1–120 phút (live); 1–7 ngày/nước (correspondence) | 20 phút |
| Byo-yomi | 1–10 kỳ × 10–60 giây | 3 × 30s |
| Fischer increment | 5–60 giây/nước | 10s |
| Xếp hạng | Có / Không | Không (ván với bạn bè) |

**Ràng buộc:** khi chấp quân > 0, komi tự đặt về 0.5 (Nhật) hoặc theo quy ước hệ luật; Đen
đặt sẵn quân chấp ở các điểm sao chuẩn rồi Trắng đi trước. Xem [02 § 8](02-go-rules-spec.md#8-chấp-quân-handicap).

## 8. Ràng buộc

| ID | Ràng buộc | Ảnh hưởng |
|----|-----------|-----------|
| C1 | Chỉ có 1 iOS dev + 1 backend dev (giả định) | Phải cắt phạm vi mạnh cho MVP, ưu tiên P0 |
| C2 | Apple bắt buộc Sign in with Apple nếu có đăng nhập bên thứ ba | Nếu thêm Google/Facebook thì phải có SwA |
| C3 | Apple bắt buộc có xóa tài khoản trong app | FR-A5 là P0, không hoãn được |
| C4 | Apple yêu cầu cơ chế report + block cho app có chat | FR-S1 là P0 |
| C5 | Background execution của iOS bị giới hạn | Không thể giữ WS sống vô hạn khi app ở nền → đồng hồ phải do server giữ (NFR-SEC3) |
| C6 | Ngân sách hạ tầng ban đầu thấp | Kiến trúc phải chạy được trên 2 node nhỏ và scale ngang khi cần ([04](04-architecture.md)) |

## 9. Giả định

> Các giả định này chưa được xác nhận với stakeholder. Nếu sai, chúng ảnh hưởng trực tiếp
> đến các quyết định trong [03-solution-design.md](03-solution-design.md).

- **A1.** Ưu tiên số một là chơi với bạn bè, không phải ghép ngẫu nhiên → matchmaking là P2.
- **A2.** Không có yêu cầu ra mắt Android trong 12 tháng tới → chọn native iOS thay vì cross-platform.
- **A3.** Không có yêu cầu AI đối thủ ở v1.0 → không cần hạ tầng GPU.
- **A4.** Không kiếm tiền ở v1.0 → chưa cần IAP, chưa cần quảng cáo.
- **A5.** Người dùng chủ yếu ở Việt Nam → deploy region Singapore (ap-southeast-1), latency ~30–50ms.

## 10. Tiêu chí nghiệm thu MVP

MVP được coi là hoàn thành khi **toàn bộ** các mục sau đúng:

- [ ] Hai thiết bị thật chơi trọn một ván 19×19 qua mạng di động, kết thúc bằng đếm điểm và ra kết quả đúng.
- [ ] Engine luật pass 100% bộ conformance vectors ở cả Swift và Go ([09](09-testing-strategy.md)).
- [ ] Replay 1.000 ván SGF chuyên nghiệp qua engine không phát sinh lỗi "nước đi bất hợp lệ".
- [ ] Giết app giữa ván rồi mở lại → ván khôi phục đúng trạng thái và đúng đồng hồ.
- [ ] Bật máy bay 60 giây giữa ván rồi tắt → ván tự khôi phục, không mất nước đi.
- [ ] Đồng hồ byo-yomi hoạt động đúng: hết kỳ cuối → xử thua, cả hai client cùng thấy kết quả.
- [ ] Xóa tài khoản hoạt động và xóa thật dữ liệu.
- [ ] Không có crash trong 100 ván test nội bộ (crash-free session ≥ 99.5%).

## 11. Câu hỏi mở

| # | Câu hỏi | Cần trả lời trước |
|---|---------|-------------------|
| Q1 | Có cần ván xếp hạng và hệ thống rank ở v1.0 không? | Chốt phạm vi Sprint 5 |
| Q2 | Có định làm Android sau không? Nếu có thì có nên tách engine luật thành lõi Rust dùng chung ngay từ đầu? | Bắt đầu code engine ([ADR-002](03-solution-design.md#adr-002--nơi-đặt-engine-luật-cờ)) |
| Q3 | Có cần chơi với AI ở v1.0 để người dùng có thể chơi khi bạn offline? | Chốt phạm vi v1.0 |
| Q4 | Mô hình kiếm tiền dự kiến (nếu có) ảnh hưởng gì tới data model? | Trước khi khóa schema |
