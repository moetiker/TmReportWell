package main

import (
	"html/template"
	"strings"
)

// Mail programs drop style sheets and flex layout, so the page is nested tables
// with inline styles. Colour never stands alone: every coloured mark has its
// word or sentence next to it.
var levelColor = []string{"#15803d", "#b45309", "#b91c1c", "#94a3b8"}

var htmlReport = template.Must(template.New("report").Funcs(template.FuncMap{
	"color":  func(l int) template.CSS { return template.CSS(levelColor[l]) },
	"title":  func() string { return reportTitle },
	"origin": origin,
	"home":   func() string { return homeURL },
	"rest":   func(n int) int { return 100 - n },
}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><meta name="color-scheme" content="light"><title>{{title}}</title></head>
<body style="margin:0;padding:0;background:#eef2f6" bgcolor="#eef2f6">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" bgcolor="#eef2f6"><tr><td align="center" style="padding:20px 12px">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:620px;font-family:-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;font-size:15px;line-height:1.5;color:#1e293b;background:#ffffff;border-radius:10px;overflow:hidden" bgcolor="#ffffff">
<tr><td style="padding:22px 26px;background:{{color .Level}};color:#ffffff">
<div style="font-size:23px;line-height:1.3;font-weight:700">{{title}}</div>
<div style="font-size:14px;padding-bottom:10px">{{.Host}}</div>
<div style="font-size:17px;line-height:1.4;font-weight:600">{{.LevelName}}: {{.Headline}}</div>
</td></tr>
{{if .Situation}}<tr><td style="padding:22px 26px 4px">
<div style="font-size:17px;font-weight:700;padding-bottom:8px">Situation</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0">
{{range .Situation}}<tr><td width="4" style="background:{{color .Level}};font-size:0" bgcolor="{{color .Level}}">&nbsp;</td><td style="padding:5px 0 5px 12px;font-size:15px;line-height:1.5;color:#1e293b">{{.Text}}</td></tr>
<tr><td colspan="2" height="6" style="font-size:0;line-height:6px">&nbsp;</td></tr>
{{end}}</table></td></tr>{{end}}
{{if .Disks}}<tr><td style="padding:14px 26px 4px">
<div style="font-size:17px;font-weight:700;padding-bottom:8px">Disks</div>
{{range .Disks}}<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="border:1px solid #d8e0e8;border-radius:8px;margin-bottom:10px"><tr><td style="padding:14px 16px;font-size:15px;line-height:1.5;color:#1e293b">
<div style="font-weight:600">{{.Name}}</div>
<div><span style="color:{{color .Level}};font-weight:700">Last backup: {{.Age}}</span>{{if .When}} <span style="color:#526072">({{.When}})</span>{{end}}</div>
{{if .Connection}}<div style="color:#526072">{{if .Connected}}<span style="color:#15803d">&#9679;</span>{{else}}<span style="color:#b45309">&#9675;</span>{{end}} {{.Connection}}</div>{{end}}
{{if ge .Percent 0}}<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin-top:8px"><tr>{{if gt .Percent 0}}<td width="{{.Percent}}%" height="8" bgcolor="#2563a8" style="background:#2563a8;font-size:0;line-height:8px">&nbsp;</td>{{end}}{{if lt .Percent 100}}<td width="{{rest .Percent}}%" height="8" bgcolor="#d8e0e8" style="background:#d8e0e8;font-size:0;line-height:8px">&nbsp;</td>{{end}}</tr></table>
<div style="font-size:13px;color:#526072;padding-top:4px">{{.Percent}}% full: {{.Used}} used, {{.Free}} free</div>{{end}}
</td></tr></table>
{{end}}</td></tr>{{end}}
<tr><td style="padding:14px 26px 4px">
<div style="font-size:17px;font-weight:700">Done</div>
{{if .Done}}{{range .Done}}<div style="padding:3px 0"><span style="color:#15803d;font-weight:700">&#10003;</span> {{.}}</div>{{end}}{{else}}<div style="color:#526072">No backup completed in this period.</div>{{end}}
</td></tr>
{{if .Settings}}<tr><td style="padding:14px 26px 4px">
<div style="font-size:17px;font-weight:700">Settings</div>
{{range .Settings}}<div style="padding:2px 0;color:#1e293b">{{.}}</div>{{end}}
</td></tr>{{end}}
{{if .Log}}<tr><td style="padding:14px 26px 4px">
<div style="font-size:17px;font-weight:700;padding-bottom:6px">Other log messages</div>
{{range .Log}}<div style="font-family:Menlo,Consolas,monospace;font-size:12px;line-height:1.5;color:#334155;background:#f3f6f9;padding:7px 10px;margin-bottom:6px;word-break:break-word">{{.}}</div>{{end}}
</td></tr>{{end}}
<tr><td style="padding:16px 26px 22px;font-size:13px;color:#526072">Period: {{.From}} to {{.To}}</td></tr>
</table>
<div style="max-width:620px;padding:14px 12px 0;font-family:-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;font-size:12px;line-height:1.5;color:#526072">{{origin}}<br><a href="{{home}}" style="color:#1d4f8c">{{home}}</a></div>
</td></tr></table></body></html>
`))

// renderHTML returns an empty string when the template fails, so the mail goes
// out as plain text instead of not at all.
func renderHTML(p page) string {
	var b strings.Builder
	if err := htmlReport.Execute(&b, p); err != nil {
		return ""
	}
	return b.String()
}
