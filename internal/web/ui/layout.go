package ui

import (
	"context"
	"fmt"
	"io"

	"github.com/a-h/templ"
)

type NavKey string

const (
	NavDashboard NavKey = "dashboard"
	NavPlugins   NavKey = "plugins"
	NavConfig    NavKey = "config"
)

// Layout renders a full HTML document.
func Layout(title string, active NavKey, body templ.Component) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		t := templ.EscapeString(title)

		_, err := fmt.Fprintf(w, "<!doctype html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\">"+
			"<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">"+
			"<title>%s</title>"+
			"<link rel=\"stylesheet\" href=\"/assets/app.css\">"+
			"</head><body>", t)
		if err != nil {
			return err
		}

		if err := topbar(ctx, w, active); err != nil {
			return err
		}

		if _, err := io.WriteString(w, "<div class=\"container\">"); err != nil {
			return err
		}
		if body != nil {
			if err := body.Render(ctx, w); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "</div></body></html>"); err != nil {
			return err
		}
		return nil
	})
}

func topbar(ctx context.Context, w io.Writer, active NavKey) error {
	_ = ctx
	if _, err := io.WriteString(w, "<div class=\"topbar\"><div class=\"topbar-inner\">"); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "<div class=\"brand\"><strong>NyaNyaBot</strong><small>Web UI</small></div>"); err != nil {
		return err
	}

	if _, err := io.WriteString(w, "<nav class=\"nav\">"); err != nil {
		return err
	}
	if err := navLink(w, "/", "仪表盘", active == NavDashboard); err != nil {
		return err
	}
	if err := navLink(w, "/plugins", "插件", active == NavPlugins); err != nil {
		return err
	}
	if err := navLink(w, "/config", "配置", active == NavConfig); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "</nav></div></div>"); err != nil {
		return err
	}
	return nil
}

func navLink(w io.Writer, href, label string, active bool) error {
	cls := ""
	if active {
		cls = " active"
	}
	_, err := fmt.Fprintf(w, "<a class=\"%s\" href=\"%s\">%s</a>", ""+cls, templ.EscapeString(href), templ.EscapeString(label))
	return err
}
