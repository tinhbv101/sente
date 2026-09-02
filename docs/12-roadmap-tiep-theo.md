# Sente — Lộ trình tiếp theo

> Lập 2026-09-01, sau khi đã có: chơi online (link + QR), thư tín + push, Sign in with Apple,
> pass-and-play, bot MCTS 4 cấp, máy đấu máy, 21 bài học, song ngữ đổi được trong app,
> TestFlight. Trạng thái: ☐ chưa làm · ◐ đang làm · ☑ xong.

## Phase A — Hoàn thiện cái đang có

| # | Hạng mục | Trạng thái |
|---|---|---|
| A1 | `PATCH /v1/devices/{token}`: bật/tắt từng loại thông báo + 4 toggle trong Cài đặt | ☑ 2026-09-01 |
| A2 | Push "sắp hết giờ" cho ván thư tín (<10% thời gian, một lần mỗi nước, dedup bằng Redis SETNX trong sweeper) | ☑ 2026-09-01 |
| A3 | Đấu lại: `POST /v1/games/{id}/rematch` copy config từ ván cũ + đảo màu; nút trong màn kết quả; đối thủ nhận push (chạm mở thẳng lời mời) và thấy trong Home | ☑ 2026-09-01 |
| A4 | Import SGF: mở file .sgf (UTI khai trong Info.plist) → màn xem lại cục bộ | ☑ 2026-09-01 |
| A5 | Phân tích ván: nút "Máy phân tích thế cờ" trong cả hai màn xem lại — MCTS 1200 lượt/2s, hiện nước đề xuất (quân ma) + tỷ lệ thắng | ☑ 2026-09-01 |

## Phase B — Giữ người chơi quay lại

| # | Hạng mục | Trạng thái |
|---|---|---|
| B1 | Tsumego mỗi ngày: 12 bài xoay vòng theo ngày (Puzzles.json, validator engine canh như bài học), chuỗi ngày, hàng riêng trên Home | ☑ 2026-09-01 |
| B2 | Thống kê cá nhân: `GET /v1/me/stats` + mục Thành tích trong Cài đặt | ☑ 2026-09-01 |
| B3 | Thang bot: hai cấp đầu mở sẵn, thắng để mở cấp sau (✓/🔒 trong màn chọn) | ☑ 2026-09-01 |
| B4 | Widget "đến lượt bạn" (WidgetKit + App Group `group.app.sente.go`; app ghi tóm tắt sau mỗi refresh) — Live Activity vẫn để sau vì cần push token riêng | ☑ 2026-09-02 |

## Phase E — Đợt 2026-09-02 ("những thứ đáng làm ngay")

| # | Hạng mục | Trạng thái |
|---|---|---|
| E1 | Bot thầy giáo: "Máy soát cả ván" trong hai màn xem lại — MCTS chấm từng thế, liệt kê 3 nước tụt tỷ lệ thắng nặng nhất, chạm để nhảy tới | ☑ 2026-09-02 |
| E2 | Chat nhanh trong ván online: bộ câu soạn sẵn (mã, không phải text tự do — không kéo theo nghĩa vụ kiểm duyệt), WS `chat` broadcast-only qua hub, bong bóng hiện 3 giây | ☑ 2026-09-02 |
| E3 | Thư viện kifu: ván local/bot kết thúc tự lưu (SGF + metadata, tối đa 200), mục "Ván đã lưu" trên Home → xem lại/phân tích/xuất SGF/xoá | ☑ 2026-09-02 |
| E4 | Âm thanh đặt quân + haptic (toggle trong Cài đặt, theo công tắc im lặng), màn chào lần đầu mở app (3 cửa: học/bot/mời bạn) | ☑ 2026-09-02 |
| E5 | Widget = B4 ở trên | ☑ 2026-09-02 |

## Phase C — Mở rộng online (khi có người chơi đều)

| # | Hạng mục | Ghi chú |
|---|---|---|
| C1 | Danh sách bạn bè (FR-A3/M4): kết bạn bằng mã, mời thẳng | tuần-công, server+app |
| C2 | Chat trong ván + kiểm duyệt (docs/08 §6) | tuần-công; kéo theo nghĩa vụ duyệt nội dung |
| C3 | ELO nội bộ | sau khi có dữ liệu ván đủ nhiều |
| C4 | Ghép ngẫu nhiên (FR-M6) | chỉ đáng khi có người online đồng thời |

> Phase C chưa khởi công có chủ đích: cả bốn mục cần lượng người chơi thật để đáng giá,
> và mỗi mục là nhiều ngày server-side. Làm A+B trước để bản TestFlight giữ được tester.

## Phase D — Trước khi nộp App Store

| # | Hạng mục | Trạng thái |
|---|---|---|
| D1 | Lỗi server (REST + WebSocket) trả theo `Accept-Language`, bảng mã lỗi → tiếng Anh | ☑ 2026-09-01 |
| D2 | App Privacy labels trên App Store Connect (theo docs/08 §7.1) | ☐ thao tác tay trên ASC |
| D3 | Quyết định thị trường VN (giấy phép G1) hoặc loại VN khỏi phát hành đợt đầu | ☐ quyết định của chủ app |
| D4 | Screenshot + mô tả App Store hai ngôn ngữ | ☐ |
