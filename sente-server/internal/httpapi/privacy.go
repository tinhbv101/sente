package httpapi

import (
	"html/template"
	"net/http"
	"strings"
)

// The privacy policy App Store Connect asks for. Served by the server so it
// lives next to the landing page and needs no separate site. It states only
// what the app actually does (docs/08 §7); when the app changes, this changes.

const privacyUpdated = "2026-08-30"

type privacyData struct {
	Lang, Other, Contact, Updated string
}

func (s *Server) handlePrivacy(w http.ResponseWriter, r *http.Request) {
	lang := pickLanguage(r)
	other := "en"
	if lang == "en" {
		other = "vi"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Language", lang)
	data := privacyData{Lang: lang, Other: other, Contact: s.config.ContactEmail, Updated: privacyUpdated}
	if err := privacyTemplate.ExecuteTemplate(w, "privacy_"+lang, data); err != nil {
		s.config.Logger.Warn("rendering privacy page", "error", err)
	}
}

// pickLanguage honours ?lang=, then Accept-Language; Vietnamese otherwise.
func pickLanguage(r *http.Request) string {
	switch r.URL.Query().Get("lang") {
	case "en":
		return "en"
	case "vi":
		return "vi"
	}
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		switch {
		case strings.HasPrefix(tag, "vi"):
			return "vi"
		case strings.HasPrefix(tag, "en"):
			return "en"
		}
	}
	return "vi"
}

var privacyTemplate = template.Must(template.New("privacy").Parse(`
{{define "head"}}<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{if eq .Lang "en"}}Sente — Privacy Policy{{else}}Sente — Chính sách quyền riêng tư{{end}}</title>
<style>
  body{margin:0;font:17px/1.6 -apple-system,"Be Vietnam Pro",system-ui,sans-serif;background:#F4F1EA;color:#17150F;padding:32px 20px}
  main{max-width:640px;margin:0 auto}
  h1{font:400 34px/1.15 Georgia,"Newsreader",serif;margin:0 0 4px}
  h2{font:600 19px/1.3 -apple-system,system-ui,sans-serif;margin:28px 0 8px}
  p,li{margin:6px 0} ul{padding-left:22px}
  .muted{color:#7B7364;font-size:14px}
  a{color:#274A73}
  nav{font-size:14px;margin-bottom:20px}
</style></head><body><main>
<nav><a href="/privacy?lang={{.Other}}">{{if eq .Lang "en"}}Tiếng Việt{{else}}English{{end}}</a></nav>{{end}}
{{define "foot"}}<p class="muted">{{if eq .Lang "en"}}Last updated{{else}}Cập nhật{{end}} {{.Updated}}</p></main></body></html>{{end}}

{{define "privacy_vi"}}{{template "head" .}}
<h1>Chính sách quyền riêng tư</h1>
<p class="muted">Sente — ứng dụng chơi cờ vây với bạn bè trên iPhone.</p>

<h2>Tóm tắt</h2>
<p>Sente không có quảng cáo, không có công cụ theo dõi của bên thứ ba, không bán hay chia sẻ dữ liệu của bạn cho mục đích tiếp thị. Chúng tôi chỉ lưu những gì cần để bạn chơi được một ván cờ với người khác, và bạn có thể xóa tài khoản ngay trong ứng dụng.</p>

<h2>Dữ liệu chúng tôi lưu</h2>
<ul>
  <li><strong>Tài khoản.</strong> Một mã định danh ngẫu nhiên, tên hiển thị (tự sinh, bạn đổi được) và mã bạn bè 8 chữ. Ứng dụng tạo tài khoản khách ngay lần mở đầu, không cần email hay số điện thoại.</li>
  <li><strong>Đăng nhập bằng Apple</strong> (tùy chọn). Mã người dùng Apple cấp riêng cho Sente và, nếu bạn chọn chia sẻ, địa chỉ email (có thể là email chuyển tiếp ẩn của Apple). Họ tên Apple gửi, nếu có, dùng làm tên hiển thị. Chúng tôi không nhận mật khẩu Apple ID.</li>
  <li><strong>Ván cờ.</strong> Các nước đi, thời điểm đi, kết quả và cấu hình ván. Đây là nội dung bạn tạo cùng đối thủ.</li>
  <li><strong>Thông báo</strong> (nếu bạn bật). Mã thiết bị do Apple cấp để gửi thông báo "đến lượt bạn", "ván kết thúc", "lời mời được nhận". Xóa khi bạn tắt hoặc xóa tài khoản.</li>
  <li><strong>Báo cáo và chặn.</strong> Khi bạn báo cáo hoặc chặn một người chơi, chúng tôi lưu lại để xử lý.</li>
  <li><strong>Nhật ký kỹ thuật.</strong> Địa chỉ IP dùng để giới hạn tần suất và chống lạm dụng; không ghi cùng mã tài khoản trong một dòng nhật ký; không có nội dung ván hay token đăng nhập trong nhật ký. Nhật ký được giữ ngắn hạn.</li>
</ul>

<h2>Những gì chúng tôi không thu thập</h2>
<p>Vị trí, danh bạ, ảnh, mã quảng cáo, hành vi dùng ứng dụng để quảng cáo. Ứng dụng không nhúng SDK phân tích hay quảng cáo nào.</p>

<h2>Camera</h2>
<p>Chỉ dùng khi bạn bấm "Quét mã QR" để đọc mã lời mời. Hình ảnh không được lưu và không rời khỏi máy.</p>

<h2>Chơi trên cùng một máy</h2>
<p>Ván chơi hai người trên một máy nằm hoàn toàn trên máy của bạn, không gửi lên máy chủ.</p>

<h2>Ai xử lý dữ liệu</h2>
<ul>
  <li><strong>Apple</strong> — Đăng nhập bằng Apple và dịch vụ thông báo đẩy (APNs), theo chính sách của Apple.</li>
  <li><strong>Cloudflare</strong> — làm lớp bảo vệ trước máy chủ; thấy địa chỉ IP của bạn khi kết nối.</li>
  <li><strong>Máy chủ Sente</strong> — do chúng tôi vận hành, lưu dữ liệu nêu trên.</li>
</ul>

<h2>Lưu bao lâu</h2>
<ul>
  <li>Tài khoản: cho tới khi bạn xóa.</li>
  <li>Ván cờ: được giữ vì ván thuộc về cả hai người chơi. Khi bạn xóa tài khoản, tên bạn trong các ván đó thành "Người chơi đã xóa".</li>
  <li>Phiên đăng nhập: tối đa 30 ngày không dùng.</li>
</ul>

<h2>Quyền của bạn</h2>
<ul>
  <li><strong>Xóa tài khoản</strong> ngay trong ứng dụng: Cài đặt → Xóa tài khoản. Tên, mã bạn bè, liên kết Apple, thiết bị thông báo và phiên đăng nhập bị xóa ngay; ván cờ được ẩn danh.</li>
  <li><strong>Đổi tên hiển thị</strong> trong Cài đặt.</li>
  <li><strong>Gỡ Đăng nhập bằng Apple</strong> trong cài đặt Apple ID của bạn; tài khoản Sente trở lại thành tài khoản khách trên máy đang dùng.</li>
  <li><strong>Xuất ván</strong> dưới dạng SGF từ màn hình xem lại.</li>
</ul>

<h2>Trẻ em</h2>
<p>Sente không nhằm tới trẻ dưới 13 tuổi và không cố ý thu thập dữ liệu của trẻ em.</p>

<h2>Thay đổi</h2>
<p>Khi chính sách thay đổi, chúng tôi cập nhật trang này và ngày ở cuối trang.</p>

{{if .Contact}}<h2>Liên hệ</h2><p>Mọi câu hỏi về dữ liệu của bạn: <a href="mailto:{{.Contact}}">{{.Contact}}</a>.</p>{{end}}
{{template "foot" .}}{{end}}

{{define "privacy_en"}}{{template "head" .}}
<h1>Privacy Policy</h1>
<p class="muted">Sente — play Go with friends on iPhone.</p>

<h2>In short</h2>
<p>Sente has no ads, no third-party trackers, and never sells or shares your data for marketing. We keep only what it takes for you to play a game with someone else, and you can delete your account inside the app.</p>

<h2>What we store</h2>
<ul>
  <li><strong>Account.</strong> A random identifier, a display name (generated; you can change it) and an 8-character friend code. The app creates a guest account on first launch; no email or phone number is needed.</li>
  <li><strong>Sign in with Apple</strong> (optional). The user identifier Apple issues to Sente and, if you choose to share it, your email address (possibly Apple's private relay address). The name Apple sends, if any, becomes your display name. We never receive your Apple ID password.</li>
  <li><strong>Games.</strong> Moves, their timestamps, results and the game settings. This is content you create together with your opponent.</li>
  <li><strong>Notifications</strong> (if you turn them on). The device token Apple issues so we can send "your move", "game over" and "invitation accepted". Removed when you turn notifications off or delete your account.</li>
  <li><strong>Reports and blocks.</strong> When you report or block a player, we keep the record to act on it.</li>
  <li><strong>Technical logs.</strong> IP addresses are used for rate limiting and abuse prevention; they are not logged on the same line as an account identifier; logs never contain game content or session tokens. Logs are kept briefly.</li>
</ul>

<h2>What we do not collect</h2>
<p>Location, contacts, photos, advertising identifiers, or usage behaviour for advertising. The app embeds no analytics or advertising SDK.</p>

<h2>Camera</h2>
<p>Used only when you tap "Scan QR code" to read an invitation. Images are not stored and never leave the device.</p>

<h2>Playing on one device</h2>
<p>Two-player games on a single device stay entirely on that device; nothing is sent to the server.</p>

<h2>Who processes data</h2>
<ul>
  <li><strong>Apple</strong> — Sign in with Apple and the push notification service (APNs), under Apple's policies.</li>
  <li><strong>Cloudflare</strong> — sits in front of the server as a protective layer and sees your IP address when you connect.</li>
  <li><strong>The Sente server</strong> — operated by us, storing the data listed above.</li>
</ul>

<h2>How long we keep it</h2>
<ul>
  <li>Account: until you delete it.</li>
  <li>Games: kept, because a game belongs to both players. When you delete your account, your name in those games becomes "Deleted player".</li>
  <li>Sessions: at most 30 days without use.</li>
</ul>

<h2>Your choices</h2>
<ul>
  <li><strong>Delete your account</strong> inside the app: Settings → Delete account. Name, friend code, Apple link, notification devices and sessions are removed immediately; games are anonymised.</li>
  <li><strong>Change your display name</strong> in Settings.</li>
  <li><strong>Stop using Sign in with Apple</strong> from your Apple ID settings; your Sente account reverts to a guest account on the device you are using.</li>
  <li><strong>Export a game</strong> as SGF from the review screen.</li>
</ul>

<h2>Children</h2>
<p>Sente is not directed at children under 13 and does not knowingly collect their data.</p>

<h2>Changes</h2>
<p>When this policy changes, we update this page and the date at the bottom.</p>

{{if .Contact}}<h2>Contact</h2><p>Questions about your data: <a href="mailto:{{.Contact}}">{{.Contact}}</a>.</p>{{end}}
{{template "foot" .}}{{end}}
`))
