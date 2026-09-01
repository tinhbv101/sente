package httpapi

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"sente.app/server/internal/metrics"
)

// Things served without a token: the invitation landing page, the file iOS reads
// to claim universal links, and the metrics scrape.

// handleLanding is what a browser sees at /j/<code> -- someone without the app,
// tapping a link a friend sent (docs/01 J1). Server-rendered, no JavaScript needed.
func (s *Server) handleLanding(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	challenge, err := s.challenges.ByCode(r.Context(), code)
	data := landingData{Code: code, AppStoreURL: s.config.AppStoreURL}
	if err == nil {
		data.Found = true
		data.Creator = challenge.CreatorName
		data.Open = challenge.Status == "pending"
		data.Size = challenge.Config.Size
		data.Rules = map[bool]string{true: "Nhật Bản", false: "Trung Quốc"}[challenge.Config.Rules == "japanese"]
		tc := challenge.Config.TimeControl
		data.Time = timeControlLabel(string(tc.Kind), int(tc.MainTime.Minutes()), tc.Periods,
			int(tc.PeriodTime.Seconds()), int(tc.PerMove.Hours()/24))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := landingTemplate.Execute(w, data); err != nil {
		s.config.Logger.Warn("rendering landing page", "error", err)
	}
}

type landingData struct {
	Code, Creator, Rules, Time, AppStoreURL string
	Size                                    int
	Found, Open                             bool
}

func timeControlLabel(kind string, minutes, periods, periodSeconds, days int) string {
	switch kind {
	case "correspondence":
		return strconv.Itoa(days) + " ngày mỗi nước"
	case "byoyomi":
		return strconv.Itoa(minutes) + " phút + " + strconv.Itoa(periods) + "×" + strconv.Itoa(periodSeconds) + " giây"
	default:
		return strconv.Itoa(minutes) + " phút"
	}
}

var landingTemplate = template.Must(template.New("landing").Parse(`<!doctype html>
<html lang="vi"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Sente — lời mời chơi cờ vây</title>
<style>
  body{margin:0;font:17px/1.5 -apple-system,"Be Vietnam Pro",system-ui,sans-serif;background:#F4F1EA;color:#17150F;display:flex;min-height:100vh;align-items:center;justify-content:center;padding:24px}
  main{max-width:420px;width:100%;text-align:center}
  h1{font:400 32px/1.15 Georgia,"Newsreader",serif;margin:0 0 8px}
  .code{font:500 30px/1 ui-monospace,Menlo,monospace;letter-spacing:.14em;background:#fff;border-radius:12px;padding:14px 20px;display:inline-block;margin:16px 0}
  dl{background:#fff;border-radius:16px;padding:6px 16px;text-align:left;margin:16px 0}
  dl div{display:flex;justify-content:space-between;padding:10px 0;border-top:1px solid #E5DFD2;font-size:15px}
  dl div:first-child{border:0} dt{color:#7B7364} dd{margin:0;font-weight:600}
  a.btn{display:block;background:#274A73;color:#fff;text-decoration:none;font-weight:600;border-radius:15px;padding:15px;margin-top:10px}
  p.muted{color:#7B7364;font-size:14px}
</style></head><body><main>
{{if .Found}}
  <h1>{{.Creator}} mời bạn<br>một ván cờ vây</h1>
  {{if .Open}}
  <p class="muted">Mở trong ứng dụng Sente, hoặc nhập mã này sau khi cài:</p>
  <div class="code">{{.Code}}</div>
  <dl>
    <div><dt>Cỡ bàn</dt><dd>{{.Size}} × {{.Size}}</dd></div>
    <div><dt>Hệ luật</dt><dd>{{.Rules}}</dd></div>
    <div><dt>Thời gian</dt><dd>{{.Time}}</dd></div>
  </dl>
  <a class="btn" href="sente://j/{{.Code}}">Mở trong Sente</a>
  {{if .AppStoreURL}}<a class="btn" style="background:#fff;color:#274A73;border:1.5px solid #A69E8D" href="{{.AppStoreURL}}">Tải Sente trên App Store</a>{{end}}
  {{else}}
  <p class="muted">Lời mời này không còn hiệu lực.</p>
  {{end}}
{{else}}
  <h1>Không tìm thấy lời mời</h1>
  <p class="muted">Mã <span class="code" style="font-size:18px;padding:6px 10px">{{.Code}}</span> không tồn tại hoặc đã hết hạn.</p>
{{end}}
<p class="muted"><a href="/privacy" style="color:#7B7364">Quyền riêng tư</a></p>
</main></body></html>`))

// handleAASA lets iOS open https://<host>/j/… and /g/… in the app. Apple fetches
// it from the domain; it must be served at exactly this path, as JSON, with no
// redirect. Empty when no Team ID is configured, so nothing wrong is claimed.
func (s *Server) handleAASA(w http.ResponseWriter, r *http.Request) {
	if s.config.AppleTeamID == "" {
		writeError(w, r, http.StatusNotFound, "not_configured", "Universal links are not configured.")
		return
	}
	appID := s.config.AppleTeamID + ".app.sente.go"
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"applinks": map[string]any{
			"details": []map[string]any{{
				"appIDs": []string{appID},
				"components": []map[string]any{
					{"/": "/j/*", "comment": "invitation"},
					{"/": "/g/*", "comment": "game"},
				},
			}},
		},
	})
}

// metricsHandler exposes the Prometheus scrape. Not rate limited and not
// authenticated: it is meant to be reached over the internal network only, and
// the reverse proxy should not route it from the outside.
func metricsHandler() http.Handler {
	_ = metrics.GameDesync // keep the package linked even if nothing else references it
	return promhttp.Handler()
}
