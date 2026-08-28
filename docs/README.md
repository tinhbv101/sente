# Sente — Cờ vây online trên iOS

Bộ tài liệu thiết kế cho ứng dụng chơi cờ vây (Go / Baduk / 囲碁) trên iOS, hỗ trợ chơi
online với bạn bè theo thời gian thực và theo lượt (correspondence).

## Tên sản phẩm

**Sente** (先手, *tiên thủ*) — nước đi buộc đối phương phải đáp, người đi giành được thế
chủ động. Đây là từ mà mọi kỳ thủ cờ vây đều biết, và nó chính là điều app nói với người
dùng mỗi ngày: *đến lượt bạn rồi*.

| | |
|---|---|
| Tên App Store | `Sente — Cờ Vây Online` |
| Subtitle | `Đến lượt bạn rồi` |
| Bundle ID | `app.sente.go` |
| Domain | `sente.app` (API: `api.sente.app`) |
| Link mời | `https://sente.app/j/<code>` · Ván: `https://sente.app/g/<game_id>` |
| Icon | Quân cờ đen với vệt sáng lệch một bên |
| Đọc là | "sen-tê" |

> **Việc cần làm trước khi khóa tên (P0).** Tra cứu nhãn hiệu trước khi đăng ký App Store
> Connect và mua domain: *Sente Technologies* (hãng game arcade thập niên 1980, khả năng
> cao đã hết hiệu lực) và *Sente* reference manager (đã ngừng phát triển). Kiểm tra
> USPTO/WIPO nhóm 9 & 41, tìm "sente" trên App Store, và xác nhận `sente.app` còn trống.
> Phương án dự phòng nếu vướng: **Hoshi** (星, điểm sao) — cùng đặc tính, rủi ro thấp hơn.

## Cách đọc tài liệu

Đọc theo thứ tự nếu bạn mới tham gia dự án. Mỗi tài liệu đứng độc lập được, nhưng
`03-solution-design.md` là nơi giải thích **vì sao** các lựa chọn kỹ thuật lại như vậy.

| # | Tài liệu | Nội dung | Đối tượng |
|---|----------|----------|-----------|
| 01 | [Requirements](01-requirements.md) | Mục tiêu sản phẩm, personas, user stories, yêu cầu chức năng & phi chức năng, phạm vi | PM, toàn team |
| 02 | [Đặc tả luật cờ vây](02-go-rules-spec.md) | Luật chơi ở mức đủ chính xác để lập trình: bắt quân, ko/superko, đếm điểm, đồng hồ | Engineer (core) |
| 03 | [Solution Design](03-solution-design.md) | Các quyết định kỹ thuật + trade-off (ADR), lựa chọn stack, thuật toán bàn cờ | Tech lead, engineer |
| 04 | [Architecture](04-architecture.md) | Kiến trúc hệ thống, thành phần, luồng dữ liệu, deployment, vận hành | Tech lead, SRE |
| 05 | [Data Model](05-data-model.md) | Schema PostgreSQL, cấu trúc Redis, lưu trữ ván cờ & SGF | Backend |
| 06 | [API & Realtime Protocol](06-api-and-realtime-protocol.md) | REST endpoints + đặc tả message WebSocket | Backend, iOS |
| 07 | [iOS App Design](07-ios-app-design.md) | Kiến trúc app, module SPM, render bàn cờ, offline/sync, accessibility | iOS |
| 08 | [Security & Fair Play](08-security-fairplay.md) | Auth, phân quyền, chống gian lận (AI assist), moderation, quyền riêng tư | Toàn team |
| 09 | [Testing Strategy](09-testing-strategy.md) | Chiến lược test, conformance vectors, load test, tiêu chí coverage | QA, engineer |
| 10 | [Roadmap](10-roadmap.md) | Phân kỳ MVP → v1.0, ước lượng, rủi ro | PM, tech lead |
| 11 | [Deployment](11-deployment.md) | Chạy server trên VPS Ubuntu, sao lưu, cập nhật | SRE, tech lead |

## Tóm tắt một trang

**Sản phẩm.** App iOS native cho phép hai người bạn chơi cờ vây với nhau: tạo ván bằng
mã mời, chơi realtime có đồng hồ, hoặc chơi theo lượt kiểu "thư tín" (mỗi nước vài giờ /
vài ngày) với push notification. Hỗ trợ bàn 9×9, 13×13, 19×19, chấp quân, hai hệ luật
đếm điểm (Nhật/Trung), xem lại ván đấu và xuất SGF.

**Nguyên tắc kiến trúc quan trọng nhất.** *Server là trọng tài duy nhất.* Client có bản
sao của engine luật để phản hồi tức thì (đặt quân thấy ngay), nhưng mọi nước đi đều được
server xác thực lại và server giữ đồng hồ. Client không bao giờ được tin tưởng.

**Stack đề xuất.**

```
iOS (Swift 6, SwiftUI, iOS 17+)   ── HTTPS/REST ──▶  API service (Go)
   └─ GoKit (rules engine, SPM)   ── WSS ────────▶  Realtime gateway (Go)
                                                      ├─ PostgreSQL (nguồn sự thật)
                                                      ├─ Redis (state cache, pub/sub, presence)
                                                      └─ APNs worker (queue)
```

**Rủi ro số một.** Hai bản engine luật (Swift ở client, Go ở server) phân kỳ với nhau →
client hiển thị một đằng, server xử một nẻo. Giải pháp: một bộ **conformance test
vectors** dạng JSON dùng chung cho cả hai bản, chạy trong CI của cả hai repo. Chi tiết ở
[03-solution-design.md § ADR-002](03-solution-design.md#adr-002--nơi-đặt-engine-luật-cờ).

## Thuật ngữ

| Tiếng Việt | Tiếng Anh | Giải thích ngắn |
|-----------|-----------|-----------------|
| Khí | Liberty | Giao điểm trống kề một quân/đám quân |
| Đám quân | Chain / String / Group | Các quân cùng màu nối liền nhau |
| Bắt quân | Capture | Lấy đi đám quân hết khí |
| Kiếp | Ko | Thế lặp vô hạn, bị luật cấm đi lại ngay |
| Nhường lượt | Pass | Không đi nước nào |
| Đất | Territory | Vùng trống được một bên vây kín |
| Cầm quân đen đi trước | Black plays first | — |
| Tiền cống / komi | Komi | Điểm bù cho Trắng vì đi sau |
| Chấp quân | Handicap | Đen đặt sẵn nhiều quân trước khi bắt đầu |
| Quân chết | Dead stone | Quân còn trên bàn nhưng không thể sống, bị tính là tù binh khi kết thúc |
| Song sinh / seki | Seki | Thế cùng sống, không bên nào đi được |
| SGF | Smart Game Format | Định dạng file chuẩn để lưu ván cờ |
