package ui

import (
	"context"
	"fmt"
	"io"
	"sort"

	"github.com/a-h/templ"
	"github.com/xiaocaoooo/nyanyabot/internal/config"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin"
)

func Dashboard(cfg config.AppConfig, plugins []plugin.Descriptor) templ.Component {
	return Layout("NyaNyaBot - Dashboard", NavDashboard, templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_ = ctx
		if _, err := io.WriteString(w, "<h1 class=\"h1\">仪表盘</h1><p class=\"sub\">查看当前运行配置与已加载插件。</p>"); err != nil {
			return err
		}

		if _, err := io.WriteString(w, "<div class=\"grid\">"); err != nil {
			return err
		}

		// Plugins card
		if _, err := io.WriteString(w, "<section class=\"card span-6\">"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "<div style=\"display:flex;align-items:center;justify-content:space-between;gap:12px;\"><div><div class=\"h1\" style=\"font-size:18px;margin:0\">插件</div><div class=\"muted\">已加载 %d 个</div></div><a class=\"btn primary\" href=\"/plugins\">查看</a></div>", len(plugins)); err != nil {
			return err
		}
		if len(plugins) > 0 {
			sample := plugins
			if len(sample) > 5 {
				sample = sample[:5]
			}
			if _, err := io.WriteString(w, "<div style=\"margin-top:12px\">"); err != nil {
				return err
			}
			for _, p := range sample {
				if _, err := fmt.Fprintf(w, "<div style=\"display:flex;align-items:center;justify-content:space-between;padding:8px 0;border-bottom:1px solid var(--border)\"><div><div>%s</div><div class=\"muted\" style=\"font-size:12px\">%s · %s</div></div><a class=\"pill\" href=\"/plugins/%s\">%s</a></div>",
					templ.EscapeString(p.Name),
					templ.EscapeString(p.Version),
					templ.EscapeString(p.Author),
					templ.EscapeString(p.PluginID),
					templ.EscapeString(p.PluginID),
				); err != nil {
					return err
				}
			}
			if _, err := io.WriteString(w, "</div>"); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "</section>"); err != nil {
			return err
		}

		// Config card
		if _, err := io.WriteString(w, "<section class=\"card span-6\">"); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "<div style=\"display:flex;align-items:center;justify-content:space-between;gap:12px;\"><div><div class=\"h1\" style=\"font-size:18px;margin:0\">配置</div><div class=\"muted\">WebUI / OneBot 监听地址</div></div><a class=\"btn primary\" href=\"/config\">编辑</a></div>"); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "<div class=\"kvs\">"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "<div class=\"k\">WebUI</div><div><code>%s</code></div>", templ.EscapeString(cfg.WebUI.ListenAddr)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "<div class=\"k\">OneBot ReverseWS</div><div><code>%s</code></div>", templ.EscapeString(cfg.OneBot.ReverseWS.ListenAddr)); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "</div>"); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "</section>"); err != nil {
			return err
		}

		if _, err := io.WriteString(w, "</div>"); err != nil {
			return err
		}
		return nil
	}))
}

func PluginsPage(plugins []plugin.Descriptor) templ.Component {
	// Stable ordering.
	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].PluginID < plugins[j].PluginID
	})

	return Layout("NyaNyaBot - Plugins", NavPlugins, templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_ = ctx
		if _, err := io.WriteString(w, "<h1 class=\"h1\">插件</h1><p class=\"sub\">当前进程内已加载的插件列表。</p>"); err != nil {
			return err
		}

		if _, err := io.WriteString(w, "<div class=\"card\">"); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "<table class=\"table\"><thead><tr><th>插件</th><th>版本</th><th>作者</th><th>能力</th></tr></thead><tbody>"); err != nil {
			return err
		}
		if len(plugins) == 0 {
			if _, err := io.WriteString(w, "<tr><td colspan=\"4\" class=\"muted\">暂无插件</td></tr>"); err != nil {
				return err
			}
		} else {
			for _, p := range plugins {
				cap := fmt.Sprintf("%d commands · %d events", len(p.Commands), len(p.Events))
				if _, err := fmt.Fprintf(w, "<tr><td><div><a href=\"/plugins/%s\"><strong>%s</strong></a></div><div class=\"muted\" style=\"font-size:12px\"><code>%s</code></div></td><td>%s</td><td>%s</td><td>%s</td></tr>",
					templ.EscapeString(p.PluginID),
					templ.EscapeString(p.Name),
					templ.EscapeString(p.PluginID),
					templ.EscapeString(p.Version),
					templ.EscapeString(p.Author),
					templ.EscapeString(cap),
				); err != nil {
					return err
				}
			}
		}
		if _, err := io.WriteString(w, "</tbody></table></div>"); err != nil {
			return err
		}
		return nil
	}))
}

func PluginDetailPage(p plugin.Descriptor) templ.Component {
	return Layout("NyaNyaBot - Plugin", NavPlugins, templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_ = ctx
		if _, err := fmt.Fprintf(w, "<h1 class=\"h1\">%s <span class=\"pill\"><code>%s</code></span></h1>", templ.EscapeString(p.Name), templ.EscapeString(p.PluginID)); err != nil {
			return err
		}
		if p.Description != "" {
			if _, err := fmt.Fprintf(w, "<p class=\"sub\">%s</p>", templ.EscapeString(p.Description)); err != nil {
				return err
			}
		}

		if _, err := io.WriteString(w, "<div class=\"grid\">"); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "<section class=\"card span-6\"><div class=\"h1\" style=\"font-size:18px;margin:0 0 10px 0\">元信息</div>"); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "<div class=\"kvs\">"); err != nil {
			return err
		}
		rows := [][2]string{
			{"版本", p.Version},
			{"作者", p.Author},
		}
		for _, r := range rows {
			if _, err := fmt.Fprintf(w, "<div class=\"k\">%s</div><div>%s</div>", templ.EscapeString(r[0]), templ.EscapeString(r[1])); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "</div></section>"); err != nil {
			return err
		}

		// Config
		if _, err := io.WriteString(w, "<section class=\"card span-6\"><div style=\"display:flex;align-items:center;justify-content:space-between;gap:12px;\"><div><div class=\"h1\" style=\"font-size:18px;margin:0\">配置</div><div class=\"muted\" style=\"font-size:12px\">插件运行中可修改并立即生效</div></div>"); err != nil {
			return err
		}
		if p.Config != nil {
			if _, err := fmt.Fprintf(w, "<a class=\"btn primary\" href=\"/plugins/%s/config\">编辑</a>", templ.EscapeString(p.PluginID)); err != nil {
				return err
			}
		} else {
			if _, err := io.WriteString(w, "<span class=\"pill\">无</span>"); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "</div>"); err != nil {
			return err
		}
		if p.Config != nil {
			ver := p.Config.Version
			if ver == "" {
				ver = "-"
			}
			if _, err := io.WriteString(w, "<div class=\"kvs\" style=\"margin-top:10px\">"); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "<div class=\"k\">版本</div><div><code>%s</code></div>", templ.EscapeString(ver)); err != nil {
				return err
			}
			if p.Config.Description != "" {
				if _, err := fmt.Fprintf(w, "<div class=\"k\">说明</div><div>%s</div>", templ.EscapeString(p.Config.Description)); err != nil {
					return err
				}
			}
			if _, err := io.WriteString(w, "</div>"); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "</section>"); err != nil {
			return err
		}

		// Commands
		if _, err := io.WriteString(w, "<section class=\"card span-6\"><div style=\"display:flex;align-items:center;justify-content:space-between\"><div class=\"h1\" style=\"font-size:18px;margin:0\">命令</div>"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "<span class=\"pill\">%d</span></div>", len(p.Commands)); err != nil {
			return err
		}
		if len(p.Commands) == 0 {
			if _, err := io.WriteString(w, "<p class=\"muted\">无</p>"); err != nil {
				return err
			}
		} else {
			if _, err := io.WriteString(w, "<div style=\"margin-top:10px\"><table class=\"table\"><thead><tr><th>ID</th><th>名称</th><th>Pattern</th><th>MatchRaw</th></tr></thead><tbody>"); err != nil {
				return err
			}
			for _, c := range p.Commands {
				if _, err := fmt.Fprintf(w, "<tr><td><code>%s</code></td><td>%s</td><td><code>%s</code></td><td>%t</td></tr>",
					templ.EscapeString(c.ID),
					templ.EscapeString(c.Name),
					templ.EscapeString(c.Pattern),
					c.MatchRaw,
				); err != nil {
					return err
				}
			}
			if _, err := io.WriteString(w, "</tbody></table></div>"); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "</section>"); err != nil {
			return err
		}

		// Events
		if _, err := io.WriteString(w, "<section class=\"card span-12\"><div style=\"display:flex;align-items:center;justify-content:space-between\"><div class=\"h1\" style=\"font-size:18px;margin:0\">事件监听</div>"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "<span class=\"pill\">%d</span></div>", len(p.Events)); err != nil {
			return err
		}
		if len(p.Events) == 0 {
			if _, err := io.WriteString(w, "<p class=\"muted\">无</p>"); err != nil {
				return err
			}
		} else {
			if _, err := io.WriteString(w, "<div style=\"margin-top:10px\"><table class=\"table\"><thead><tr><th>ID</th><th>名称</th><th>Event</th></tr></thead><tbody>"); err != nil {
				return err
			}
			for _, e := range p.Events {
				if _, err := fmt.Fprintf(w, "<tr><td><code>%s</code></td><td>%s</td><td><code>%s</code></td></tr>", templ.EscapeString(e.ID), templ.EscapeString(e.Name), templ.EscapeString(e.Event)); err != nil {
					return err
				}
			}
			if _, err := io.WriteString(w, "</tbody></table></div>"); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "</section></div>"); err != nil {
			return err
		}
		return nil
	}))
}

func PluginConfigPage(p plugin.Descriptor, currentJSON string, fieldsHTML string, flashOK string, flashErr string) templ.Component {
	return Layout("NyaNyaBot - Plugin Config", NavPlugins, templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_ = ctx
		if _, err := fmt.Fprintf(w, "<h1 class=\"h1\">配置：%s <span class=\"pill\"><code>%s</code></span></h1>", templ.EscapeString(p.Name), templ.EscapeString(p.PluginID)); err != nil {
			return err
		}
		if p.Description != "" {
			if _, err := fmt.Fprintf(w, "<p class=\"sub\">%s</p>", templ.EscapeString(p.Description)); err != nil {
				return err
			}
		}
		if flashOK != "" {
			if _, err := fmt.Fprintf(w, "<div class=\"flash good\">%s</div>", templ.EscapeString(flashOK)); err != nil {
				return err
			}
		}
		if flashErr != "" {
			if _, err := fmt.Fprintf(w, "<div class=\"flash bad\">%s</div>", templ.EscapeString(flashErr)); err != nil {
				return err
			}
		}

		if p.Config == nil {
			if _, err := io.WriteString(w, "<div class=\"card\"><p class=\"muted\">该插件未声明可配置项（Descriptor.config 为空）。</p><a class=\"btn primary\" href=\"/plugins/\">返回</a></div>"); err != nil {
				return err
			}
			return nil
		}

		if _, err := io.WriteString(w, "<div class=\"grid\">"); err != nil {
			return err
		}

		// Editor (schema-driven)
		if _, err := io.WriteString(w, "<section class=\"card span-6\"><div class=\"h1\" style=\"font-size:18px;margin:0 0 10px 0\">编辑配置</div>"); err != nil {
			return err
		}
		// Note: schema-driven form fields are injected by server as pre-rendered HTML rows.
		// If schema parsing fails, server will fall back to JSON textarea.
		if _, err := io.WriteString(w, "<form class=\"form\" method=\"post\" action=\"/plugins/"+templ.EscapeString(p.PluginID)+"/config\">"+
			fieldsHTML+
			"<div style=\"display:flex;gap:10px;align-items:center;flex-wrap:wrap\">"+
			"<button class=\"btn primary\" type=\"submit\" name=\"action\" value=\"save\">保存并立即生效</button>"+
			"<button class=\"btn\" type=\"submit\" name=\"action\" value=\"reset\">重置为默认</button>"+
			"<a class=\"btn\" href=\"/plugins/"+templ.EscapeString(p.PluginID)+"\">返回详情</a>"+
			"</div></form></section>"); err != nil {
			return err
		}

		// Schema / Default
		if _, err := io.WriteString(w, "<section class=\"card span-6\"><div class=\"h1\" style=\"font-size:18px;margin:0 0 10px 0\">配置规范</div>"); err != nil {
			return err
		}
		ver := p.Config.Version
		if ver == "" {
			ver = "-"
		}
		if _, err := io.WriteString(w, "<div class=\"kvs\">"+
			"<div class=\"k\">版本</div><div><code>"+templ.EscapeString(ver)+"</code></div>"); err != nil {
			return err
		}
		if p.Config.Description != "" {
			if _, err := fmt.Fprintf(w, "<div class=\"k\">说明</div><div>%s</div>", templ.EscapeString(p.Config.Description)); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "</div>"); err != nil {
			return err
		}

		schema := string(p.Config.Schema)
		if schema == "" {
			schema = "{}"
		}
		def := string(p.Config.Default)
		if def == "" {
			def = "{}"
		}
		if _, err := io.WriteString(w, "<div style=\"margin-top:12px\"><div class=\"muted\" style=\"margin-bottom:6px\">Schema</div>"+
			"<pre class=\"pre\"><code>"+templ.EscapeString(schema)+"</code></pre></div>"); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "<div style=\"margin-top:12px\"><div class=\"muted\" style=\"margin-bottom:6px\">Default</div>"+
			"<pre class=\"pre\"><code>"+templ.EscapeString(def)+"</code></pre></div>"); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "</section>"); err != nil {
			return err
		}

		if _, err := io.WriteString(w, "</div>"); err != nil {
			return err
		}
		return nil
	}))
}

func ConfigPage(cfg config.AppConfig, flashOK string, flashErr string) templ.Component {
	return Layout("NyaNyaBot - Config", NavConfig, templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_ = ctx
		if _, err := io.WriteString(w, "<h1 class=\"h1\">配置</h1><p class=\"sub\">修改 WebUI 与 OneBot ReverseWS 的监听地址。</p>"); err != nil {
			return err
		}
		if flashOK != "" {
			if _, err := fmt.Fprintf(w, "<div class=\"flash good\">%s</div>", templ.EscapeString(flashOK)); err != nil {
				return err
			}
		}
		if flashErr != "" {
			if _, err := fmt.Fprintf(w, "<div class=\"flash bad\">%s</div>", templ.EscapeString(flashErr)); err != nil {
				return err
			}
		}

		if _, err := io.WriteString(w, "<div class=\"card\">"); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "<form class=\"form\" method=\"post\" action=\"/config\">"); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "<div class=\"field\"><label for=\"webui_listen_addr\">WebUI 监听地址</label>"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "<input id=\"webui_listen_addr\" name=\"webui_listen_addr\" value=\"%s\" placeholder=\"127.0.0.1:3000\"></div>", templ.EscapeString(cfg.WebUI.ListenAddr)); err != nil {
			return err
		}

		if _, err := io.WriteString(w, "<div class=\"field\"><label for=\"onebot_reverse_ws_listen_addr\">OneBot ReverseWS 监听地址</label>"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "<input id=\"onebot_reverse_ws_listen_addr\" name=\"onebot_reverse_ws_listen_addr\" value=\"%s\" placeholder=\"0.0.0.0:3001\"></div>", templ.EscapeString(cfg.OneBot.ReverseWS.ListenAddr)); err != nil {
			return err
		}

		if _, err := io.WriteString(w, "<div style=\"display:flex;gap:10px;align-items:center\"><button class=\"btn primary\" type=\"submit\">保存</button><span class=\"muted\">保存后需要重启服务以生效</span></div>"); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "</form></div>"); err != nil {
			return err
		}
		return nil
	}))
}

func NotFoundPage(path string) templ.Component {
	return Layout("NyaNyaBot - Not Found", NavDashboard, templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_ = ctx
		if _, err := io.WriteString(w, "<div class=\"card\"><h1 class=\"h1\">404</h1>"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "<p class=\"sub\">页面不存在：<code>%s</code></p>", templ.EscapeString(path)); err != nil {
			return err
		}
		_, err := io.WriteString(w, "<a class=\"btn primary\" href=\"/\">返回仪表盘</a></div>")
		return err
	}))
}
