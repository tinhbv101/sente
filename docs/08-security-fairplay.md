# 08 — Security & Fair Play

> Bảo mật kỹ thuật, chống gian lận, kiểm duyệt và quyền riêng tư.
> Yêu cầu gốc ở [01 §4.6 và §5.4](01-requirements.md#46-an-toàn--kiểm-duyệt).

## 1. Mô hình mối đe dọa

Xếp theo mức độ thực tế đối với app này, không theo lý thuyết.

| # | Mối đe dọa | Tác nhân | Tác động | Khả năng | Xử lý |
|---|-----------|----------|----------|----------|-------|
| T1 | Đi nước sai luật / đi thay đối thủ bằng client sửa đổi | Người chơi | Phá hỏng ván, mất niềm tin | **Cao** | [§3](#3-server-là-trọng-tài) |
| T2 | Gian lận thời gian (giả report còn giờ, làm chậm gói tin) | Người chơi | Thắng không công bằng | Trung bình | [§3.2](#32-đồng-hồ) |
| T3 | Dùng AI (KataGo) hỗ trợ khi chơi | Người chơi | Phá hỏng trải nghiệm đối thủ | **Cao** ở ván xếp hạng, thấp ở ván với bạn | [§5](#5-chống-gian-lận) |
| T4 | Chiếm tài khoản qua đánh cắp token | Kẻ tấn công | Mất lịch sử, mạo danh | Trung bình | [§2](#2-xác-thực-và-quản-lý-phiên) |
| T5 | Quấy rối qua chat và tên hiển thị | Người chơi | Rủi ro an toàn người dùng + App Store từ chối | **Cao** | [§6](#6-kiểm-duyệt) |
| T6 | Spam tạo tài khoản / lời mời | Bot | Tốn tài nguyên, spam người thật | Trung bình | [§4](#4-rate-limit-và-chống-lạm-dụng) |
| T7 | Enumerate friend code / user id | Kẻ tấn công | Lộ danh sách người dùng | Thấp | [§4.2](#42-chống-liệt-kê) |
| T8 | Rò rỉ dữ liệu cá nhân trong log/push | Nội bộ | Vi phạm quyền riêng tư | Trung bình | [§7](#7-quyền-riêng-tư) |
| T9 | Bỏ ván khi sắp thua (escaping) | Người chơi | Phí thời gian đối thủ | **Cao** | [§5.3](#53-bỏ-ván-t9) |
| T10 | Lộ bí mật hệ thống (JWT key, APNs `.p8`) | Nội bộ / rò rỉ repo | Nghiêm trọng | Thấp | [§8](#8-quản-lý-bí-mật) |

**T3 có ngữ cảnh riêng cho sản phẩm này.** Vì trọng tâm v1.0 là chơi với bạn bè
([A1](01-requirements.md#9-giả-định)), động cơ dùng AI thấp và người bị hại có thể tự xử lý
(không chơi với người đó nữa). Đầu tư nặng vào phát hiện AI chỉ đáng khi có ván xếp hạng —
vì vậy nó là **P2** ([FR-S4](01-requirements.md#46-an-toàn--kiểm-duyệt)).

## 2. Xác thực và quản lý phiên

### 2.1 Token

| Token | Thời hạn | Lưu ở client | Ghi chú |
|-------|----------|--------------|---------|
| Access token (JWT) | 15 phút | Bộ nhớ (không ghi đĩa) | `HS256` với khóa trong Secrets Manager, hoặc `EdDSA` nếu cần verify phân tán |
| Refresh token | 30 ngày, quay vòng | Keychain, `kSecAttrAccessibleAfterFirstUnlock` | Chỉ lưu SHA-256 ở server |
| Device key (Ed25519) | Vĩnh viễn | Keychain, **không** đồng bộ iCloud | Danh tính tài khoản khách |

**JWT claims:**

```json
{
  "sub": "018f...",          // user id
  "iat": 1756377600,
  "exp": 1756378500,
  "jti": "01J8X...",         // để thu hồi cá biệt nếu cần
  "ver": 1                   // token schema version
}
```

Không nhét quyền hay dữ liệu người dùng vào JWT — mọi phân quyền tra lại DB/cache. JWT chỉ
trả lời "ai đang gọi", không trả lời "được làm gì".

### 2.2 Refresh token quay vòng

```
Client dùng RT₁ → server cấp AT₂ + RT₂, đánh dấu RT₁ đã dùng
Client dùng RT₁ lần nữa (token đã bị đánh cắp hoặc client lỗi)
  → server phát hiện reuse → THU HỒI TOÀN BỘ family
  → mọi phiên của user bị đăng xuất, buộc đăng nhập lại
```

`family_id` trong bảng `refresh_tokens` ([05 §3](05-data-model.md#3-người-dùng-và-danh-tính))
phục vụ đúng việc này. Có nguy cơ dương tính giả khi client gặp race (hai request refresh
đồng thời) — giảm nhẹ bằng cửa sổ ân hạn 10 giây: dùng lại RT₁ trong 10 giây đầu trả về
đúng cặp token đã cấp, không coi là tấn công.

### 2.3 Tài khoản khách và DeviceCheck

```
POST /v1/auth/guest gửi kèm DeviceCheck token
  → server verify với Apple
  → mỗi thiết bị vật lý chỉ tạo được 3 tài khoản khách / 24 giờ
```

Không dùng `identifierForVendor` làm danh tính (đổi khi gỡ cài app). Danh tính là **cặp khóa
trong Keychain**, tồn tại qua việc gỡ/cài lại app.

### 2.4 Sign in with Apple

- Verify `identity_token` bằng khóa công khai của Apple (`https://appleid.apple.com/auth/keys`),
  cache JWKS 24 giờ.
- Kiểm tra `aud` = bundle id, `iss` = `https://appleid.apple.com`, `exp` còn hạn, `nonce`
  khớp với nonce client gửi ban đầu.
- **Bắt buộc hỗ trợ "Ẩn email của tôi"** — không được yêu cầu email thật.
- Xử lý webhook thu hồi của Apple (`server-to-server notifications`): người dùng gỡ liên kết
  app ở cài đặt Apple ID ⇒ hạ tài khoản về trạng thái khách, không xóa dữ liệu.

> **Sửa lại khi cài đặt (2026-08-29).** Đã làm đúng như trên, trong `internal/apple`:
> JWKS cache 24 giờ và tự lấy lại **một lần** khi gặp `kid` lạ (Apple xoay khóa), chỉ nhận
> `RS256`, từ chối `alg=none`. Nonce: app gửi giá trị gốc, server so với SHA-256 trong token.
> Webhook nhận ở `POST /v1/auth/apple/notifications`, cũng verify chữ ký Apple; khi thu hồi
> thì gỡ liên kết **và thu hồi mọi refresh token** của tài khoản — access token còn sống tối đa
> 15 phút. Khóa `.p8` của Sign in with Apple chỉ cần cho việc gọi token endpoint của Apple
> (`apple.ClientSecret`), chưa dùng ở đường xử lý nào; verify đăng nhập không cần khóa riêng.
> Chưa làm: DeviceCheck/App Attest khi tạo khách (§2.3).

### 2.5 Phân quyền

Nguyên tắc: **mọi endpoint đều kiểm tra quan hệ với tài nguyên**, không chỉ kiểm tra đã đăng nhập.

```go
// Never trust a game_id from the client to imply access. A user may read a game
// they play in or spectate; only the player whose turn it is may move.
func (s *Service) authorizeMove(ctx context.Context, userID UserID, g *Game) error {
    colour, ok := g.ColourOf(userID)
    if !ok {
        return ErrForbidden
    }
    if g.ToPlay != colour {
        return ErrNotYourTurn
    }
    return nil
}
```

Kiểm thử bắt buộc: với mỗi endpoint có `{id}`, phải có test "người dùng B gọi tài nguyên của
A → 403/404". Đây là lớp lỗ hổng phổ biến nhất (IDOR).

## 3. Server là trọng tài

Đây là biện pháp phòng thủ chính cho T1 và T2, và nó là **quyết định kiến trúc, không phải
tính năng bảo mật thêm vào** (Nguyên tắc 1 ở [03](03-solution-design.md#nguyên-tắc-thiết-kế)).

### 3.1 Nước đi

Server chạy lại toàn bộ kiểm tra luật cho mọi nước đi, không tin bất kỳ trường nào từ client:

| Trường client gửi | Server dùng để làm gì |
|-------------------|----------------------|
| `point` | Nước đi đề xuất — **kiểm tra lại** hoàn toàn |
| `expected_move_no` | Chỉ để phát hiện lệch, **không** để xác định vị trí trong ván |
| `client_move_id` | Chỉ để khử trùng lặp |
| `kind` | Kiểm tra hợp lệ với `phase` hiện tại |
| Bất kỳ trường nào khác về trạng thái bàn cờ | **Bỏ qua hoàn toàn** |

Client không gửi bàn cờ, không gửi tù binh, không gửi điểm số — server có tất cả.

### 3.2 Đồng hồ

- Thời gian trừ đi tính bằng đồng hồ **monotonic của server**, từ `turn_started_at` tới lúc
  nhận được message ([02 §7.2](02-go-rules-spec.md#72-trừ-thời-gian)).
- Client không gửi thời gian còn lại; nếu có gửi thì bị bỏ qua.
- Bù trễ mạng cố định `LAG_GRACE_MS = 500` mỗi nước, **có trần tổng 30 giây mỗi bên mỗi ván**.
  Trần này chặn kiểu lạm dụng "cố tình làm chậm mọi gói tin để lấy nửa giây mỗi nước": trong
  ván 200 nước, lợi ích tối đa là 30 giây thay vì 100 giây.
- Hết giờ do **timer phía server** kích hoạt, không chờ client báo.

### 3.3 Đếm điểm

Điểm số do server tính. Client tính song song chỉ để hiển thị tức thì; nếu lệch, client lấy
giá trị của server và ghi log cảnh báo (dấu hiệu lệch engine).

## 4. Rate limit và chống lạm dụng

### 4.1 Hạn mức

Bảng hạn mức ở [06 §1.3](06-api-and-realtime-protocol.md#13-rate-limit). Cài đặt bằng token
bucket trong Redis, khóa theo `user_id` (đã đăng nhập) hoặc `IP` (chưa đăng nhập), cộng thêm
lớp WAF ở ALB chặn theo IP ở mức thô.

Với WebSocket: 30 message / 10 giây / kết nối. Vượt → cảnh báo một lần, vượt tiếp → đóng
kết nối mã `4004`.

### 4.2 Chống liệt kê

- `friend_code` là 8 ký tự từ bộ 32 ký tự = 2⁴⁰ khả năng. Kèm rate limit 10 lần tra/phút →
  không khả thi để quét.
- `GET /v1/users/by-code/{code}` trả về **cùng một lỗi 404 với cùng thời gian phản hồi** cho
  cả mã không tồn tại và mã của người đã chặn mình.
- Không có endpoint nào liệt kê người dùng.
- `challenge.code` cũng 8 ký tự; lời mời hết hạn sau 7 ngày, giới hạn 20 lời mời/giờ.

### 4.3 Chống spam nội dung

- Chat: tối đa 20 tin/phút, 500 ký tự/tin, chặn URL ở v1.0 (không cần thiết trong ván cờ,
  và là vector lừa đảo phổ biến nhất).
- Tên hiển thị: lọc danh sách từ cấm (tiếng Việt + tiếng Anh), chặn ký tự vô hình
  (zero-width, RTL override), chuẩn hóa Unicode NFKC trước khi kiểm tra.

## 5. Chống gian lận

### 5.1 Dùng AI hỗ trợ (T3) — P2

**Không thể ngăn chặn** — người chơi có thể chạy KataGo trên máy tính bên cạnh. Chỉ có thể
**phát hiện và giới hạn thiệt hại**. Kế hoạch cho ván xếp hạng:

| Tín hiệu | Cách đo | Sức mạnh |
|----------|---------|----------|
| Trùng khớp nước đi | Tỉ lệ nước trùng với top-1 của KataGo (visits thấp) so với mức kỳ vọng ở hạng đó | Mạnh nhất |
| Phân bố thời gian suy nghĩ | Người thật suy nghĩ lâu ở thế phức tạp; người copy máy có thời gian đều bất thường | Trung bình |
| Nhảy vọt sức mạnh | Sức chơi tăng đột ngột giữa ván hoặc giữa các ván | Trung bình |
| Nước "chỉ AI mới đi" | Các nước lạ mà con người ở hạng đó gần như không nghĩ tới | Yếu (dương tính giả cao) |

Quy trình: chấm điểm nghi ngờ **ngoại tuyến** (batch job sau ván), gắn cờ khi vượt ngưỡng,
**người xem xét** trước khi hành động. **Không bao giờ** tự động khóa tài khoản dựa trên
thuật toán — dương tính giả với người chơi mạnh là chắc chắn xảy ra.

Ván với bạn bè (không xếp hạng) không được chấm — không cần và tốn tài nguyên.

### 5.2 Client sửa đổi (T1)

Đã vô hiệu hóa hoàn toàn bởi [§3](#3-server-là-trọng-tài). Người dùng jailbreak sửa app chỉ
tự làm hỏng hiển thị của chính mình; server từ chối mọi nước đi sai luật.

Không dùng jailbreak detection: nó chặn được người dùng hợp pháp, dễ vượt qua, và không cần
thiết khi server đã là trọng tài.

### 5.3 Bỏ ván (T9)

| Mức | Điều kiện | Hành động |
|-----|-----------|-----------|
| Bình thường | Mất kết nối < 2 phút | Không gì (chuyện thường) |
| Cảnh báo | Rời ván khi đang thua > 20 điểm, 3 lần trong 7 ngày | Nhắc nhở trong app |
| Hạn chế | 10 lần trong 30 ngày | Không tạo được ván xếp hạng trong 7 ngày |

Ván bỏ dở tự kết thúc bằng `timeout` theo đồng hồ thông thường — người bỏ ván thua vì hết
giờ, đó đã là hình phạt tự nhiên. Với ván correspondence, deadline dài nên bổ sung
[FR-G14](01-requirements.md#43-chơi-ván-core): hủy sau 30 ngày im lặng, ghi `abandonment`.

## 6. Kiểm duyệt

Bắt buộc theo App Store Guideline 1.2 vì app có nội dung do người dùng tạo (chat, tên hiển thị).

**Bốn thứ phải có trước khi nộp app:**

1. **Lọc tự động** — danh sách từ cấm cho tên hiển thị và chat.
2. **Báo cáo** — `POST /v1/reports`, truy cập được từ trong ván và từ hồ sơ đối thủ.
3. **Chặn** — chặn người chơi, có hiệu lực ngay.
4. **Cam kết xử lý trong 24 giờ** — cần một người trực hàng ngày và một công cụ admin tối
   thiểu (danh sách report, xem ván, ẩn chat, khóa tài khoản).

Công cụ admin ở v1.0 có thể chỉ là các câu lệnh CLI + truy vấn SQL có sẵn — nhưng phải tồn
tại và được ghi lại (mọi hành động admin ghi vào `game_events` với `kind='admin_intervention'`).

## 7. Quyền riêng tư

### 7.1 Dữ liệu thu thập

| Dữ liệu | Mục đích | Gắn với danh tính? | Khai báo Privacy Label |
|---------|----------|--------------------|------------------------|
| Tên hiển thị, mã bạn bè | Chức năng cốt lõi | Có | Identifiers → App Functionality |
| Ván cờ và nước đi | Chức năng cốt lõi | Có | User Content → App Functionality |
| Chat | Chức năng cốt lõi + kiểm duyệt | Có | User Content → App Functionality |
| APNs token | Thông báo | Có | Identifiers → App Functionality |
| Apple `sub` | Đăng nhập | Có | Identifiers → App Functionality |
| Crash/diagnostics | Sửa lỗi | **Không** | Diagnostics → App Functionality |

**Không thu thập:** vị trí, danh bạ, ảnh, IDFA, hành vi để quảng cáo. Không có SDK theo dõi
của bên thứ ba ([ADR-012](03-solution-design.md#adr-012--mời-bạn-qua-universal-link-có-xử-lý-deferred)).

### 7.2 Quy tắc log

```
KHÔNG BAO GIỜ ghi vào log: nội dung chat, access/refresh token, Apple identity token,
                           APNs token đầy đủ, email, IP kèm user_id trong cùng dòng
ĐƯỢC ghi:                  trace_id, user_id, game_id, move_no, mã lỗi, thời lượng
```

Push notification không chứa nội dung chat ([ADR-013](03-solution-design.md#adr-013--thông-báo-đẩy-qua-apns-với-token-key))
— màn hình khóa là nơi công khai.

### 7.3 Xóa tài khoản

[FR-A5](01-requirements.md#41-tài-khoản--danh-tính) là bắt buộc theo App Store. Chi tiết
thực thi ở [05 §14](05-data-model.md#14-vòng-đời-dữ-liệu-và-quyền-riêng-tư). Điểm cần nhấn:

- Luồng xóa phải ở trong app, không được chỉ dẫn ra web.
- Xác nhận hai bước với cảnh báo rõ ràng điều gì bị mất.
- Ván cờ **được giữ ở dạng ẩn danh** — vì ván có hai người, không thể xóa lịch sử của đối
  phương. Điều này phải được nói rõ trong màn hình xác nhận và trong privacy policy.

## 8. Quản lý bí mật

| Bí mật | Lưu ở đâu | Xoay vòng |
|--------|-----------|-----------|
| JWT signing key | AWS Secrets Manager | 90 ngày, hỗ trợ hai khóa song song khi chuyển |
| APNs `.p8` key | AWS Secrets Manager | Khi có sự cố |
| DB password | Secrets Manager, xoay tự động qua RDS | 30 ngày |
| Redis auth token | Secrets Manager | 90 ngày |
| Bảng Zobrist | **Không phải bí mật** — nằm trong repo | Không bao giờ (đổi = breaking) |

- Nạp bí mật lúc khởi động, giữ trong bộ nhớ, không bao giờ vào biến môi trường xuất hiện
  trong `docker inspect`, log khởi động, hay crash dump.
- CI dùng OIDC để lấy quyền AWS tạm thời, không có khóa dài hạn trong GitHub Secrets.
- `gitleaks` chạy ở pre-commit hook và trong CI.

## 9. Bảo mật chuỗi cung ứng

| Thành phần | Biện pháp |
|-----------|-----------|
| iOS | **Không có dependency ngoài ở target production** ([07 §1](07-ios-app-design.md#1-nền-tảng)) — đây là biện pháp bảo mật chuỗi cung ứng mạnh nhất có thể |
| Go | `go.sum` cố định, `govulncheck` trong CI, Dependabot cho dependency trực tiếp |
| Docker | Base image `gcr.io/distroless/static`, build từ scratch, không shell trong image |
| CI | Pin action theo SHA, không dùng `@main` hay `@v3` |

## 10. Bảo mật vận hành

- Truy cập prod qua SSM Session Manager, không có SSH key, không có bastion mở.
- Truy cập DB prod chỉ đọc qua read replica, cần phê duyệt cho quyền ghi.
- Mọi truy vấn thủ công lên prod ghi vào audit log.
- MFA bắt buộc cho tài khoản AWS và Apple Developer.
- Diễn tập khôi phục từ backup ít nhất một lần trước khi lên prod
  ([04 §9.3](04-architecture.md#93-runbook-cần-viết-trước-khi-lên-prod)).

## 11. Checklist trước khi phát hành

Áp dụng quy ước bảo mật của team, cụ thể hóa cho app này:

- [ ] Không có bí mật hard-code (kiểm bằng `gitleaks` trên toàn bộ lịch sử git).
- [ ] Mọi input từ client được validate bằng struct có ràng buộc, không parse thủ công.
- [ ] Mọi truy vấn SQL dùng tham số hóa (dùng `pgx` với `$1`, không nối chuỗi).
- [ ] Không có XSS: landing page escape mọi dữ liệu từ challenge (tên người mời).
- [ ] Có test IDOR cho **mọi** endpoint có `{id}`.
- [ ] Rate limit hoạt động ở cả REST và WebSocket (có test).
- [ ] TLS 1.3, HSTS trên domain web, `wss://` bắt buộc.
- [ ] Thông báo lỗi không rò rỉ chi tiết nội bộ (không trả stack trace, không trả tên bảng).
- [ ] Server là trọng tài: có test "client gửi nước đi sai luật → server từ chối" cho từng
      loại vi phạm ([06 §3.6](06-api-and-realtime-protocol.md#36-từ-chối-nước-đi)).
- [ ] Xóa tài khoản hoạt động end-to-end và đã kiểm tra dữ liệu thực sự biến mất.
- [ ] Privacy Label khai báo khớp với dữ liệu thực sự thu thập.
- [ ] Report + block hoạt động và có người trực xử lý.
- [ ] `govulncheck` sạch, Dependabot không có cảnh báo mức cao.
- [ ] Đã chạy `security-reviewer` trên toàn bộ code xử lý auth, input người dùng và endpoint.
