package ui

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

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

func ConfigPage(cfg config.AppConfig, mode string, flashConfigOK string, flashConfigErr string, flashGlobalsOK string, flashGlobalsErr string) templ.Component {
	return Layout("NyaNyaBot - Config", NavConfig, templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_ = ctx
		if _, err := io.WriteString(w, "<h1 class=\"h1\">配置</h1><p class=\"sub\">修改 WebUI 与 OneBot ReverseWS 的监听地址，并管理全局变量。</p>"); err != nil {
			return err
		}
		if flashConfigOK != "" {
			if _, err := fmt.Fprintf(w, "<div class=\"flash good\">%s</div>", templ.EscapeString(flashConfigOK)); err != nil {
				return err
			}
		}
		if flashConfigErr != "" {
			if _, err := fmt.Fprintf(w, "<div class=\"flash bad\">%s</div>", templ.EscapeString(flashConfigErr)); err != nil {
				return err
			}
		}

		if _, err := io.WriteString(w, "<div class=\"grid\">"); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "<section class=\"card span-12\"><div class=\"h1\" style=\"font-size:18px;margin:0 0 10px 0\">基础配置</div>"); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "<form class=\"form\" method=\"post\" action=\"/config\">"+
			"<input type=\"hidden\" name=\"section\" value=\"config\">"); err != nil {
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
		if _, err := io.WriteString(w, "</form></section>"); err != nil {
			return err
		}

		if err := globalsSection(cfg.Globals, mode, flashGlobalsOK, flashGlobalsErr, "/config#globals", "/config#globals").Render(ctx, w); err != nil {
			return err
		}

		if _, err := io.WriteString(w, "</div>"); err != nil {
			return err
		}
		return nil
	}))
}

func globalsSection(globals map[string]string, mode string, flashOK string, flashErr string, formAction string, toggleAction string) templ.Component {
	if globals == nil {
		globals = map[string]string{}
	}
	mode = strings.TrimSpace(mode)
	if mode != "env" {
		mode = "table"
	}
	switchChecked := ""
	if mode == "env" {
		switchChecked = " checked"
	}
	keys := make([]string, 0, len(globals))
	for k := range globals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var envB strings.Builder
	for _, k := range keys {
		envB.WriteString(k)
		envB.WriteString("=")
		envB.WriteString(globals[k])
		envB.WriteString("\n")
	}
	envText := strings.TrimRight(envB.String(), "\n")

	// Table rows: existing + 1 blank row (no fixed limit; user can add/remove rows).
	type row struct {
		Idx int
		K   string
		V   string
		On  bool
	}
	rows := make([]row, 0, len(keys)+1)
	idx := 0
	for _, k := range keys {
		rows = append(rows, row{Idx: idx, K: k, V: globals[k], On: true})
		idx++
	}
	// Always provide one empty enabled row for quick add.
	rows = append(rows, row{Idx: idx, K: "", V: "", On: true})
	nextIdx := idx + 1

	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_ = ctx
		if _, err := io.WriteString(w, "<section class=\"card span-12\" id=\"globals\">"+
			"<div style=\"display:flex;align-items:flex-start;justify-content:space-between;gap:12px;flex-wrap:wrap\">"+
			"<div><div class=\"h1\" style=\"font-size:18px;margin:0\">全局变量</div>"+
			"<div class=\"muted\" style=\"font-size:12px;margin-top:4px\">用于插件配置模板替换：<code>${name}</code>。如需原样保留，请写 <code>\\${name}</code>。</div></div>"); err != nil {
			return err
		}

		// Editor header + switch (GET)
		if _, err := fmt.Fprintf(w, "<form method=\"get\" action=\"%s\" style=\"display:flex;align-items:center;gap:10px\">"+
			"<label class=\"toggle\"><input type=\"checkbox\" name=\"mode\" value=\"env\""+switchChecked+" onchange=\"this.form.submit()\"><span class=\"slider\"></span></label>"+
			"<span class=\"muted\" style=\"font-size:12px\">编辑原始环境变量</span>"+
			"</form>"+
			"</div>", templ.EscapeString(toggleAction)); err != nil {
			return err
		}

		if flashOK != "" {
			if _, err := fmt.Fprintf(w, "<div class=\"flash good\" style=\"margin-top:12px\">%s</div>", templ.EscapeString(flashOK)); err != nil {
				return err
			}
		}
		if flashErr != "" {
			if _, err := fmt.Fprintf(w, "<div class=\"flash bad\" style=\"margin-top:12px\">%s</div>", templ.EscapeString(flashErr)); err != nil {
				return err
			}
		}

		if mode == "env" {
			if _, err := fmt.Fprintf(w, "<div style=\"margin-top:12px\">"+
				"<form class=\"form\" method=\"post\" action=\"%s\">"+
				"<input type=\"hidden\" name=\"section\" value=\"globals\">"+
				"<input type=\"hidden\" name=\"mode\" value=\"env\">", templ.EscapeString(formAction)); err != nil {
				return err
			}
			if _, err := io.WriteString(w,
				"<div class=\"field\"><label for=\"globals_env\">环境变量</label>"+
					"<div class=\"help\">每行一条：<code>KEY=value</code>；支持 <code>#</code> 注释与空行。</div>"); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "<textarea id=\"globals_env\" name=\"globals_env\" rows=\"16\" spellcheck=\"false\" placeholder=\"NAME=value\\nFOO=bar\">%s</textarea></div>", templ.EscapeString(envText)); err != nil {
				return err
			}
			if _, err := io.WriteString(w, "<div style=\"display:flex;gap:10px;align-items:center\">"+
				"<button class=\"btn primary\" type=\"submit\">保存</button>"+
				"<a class=\"btn\" href=\"/\">返回仪表盘</a>"+
				"</div></form></div>"); err != nil {
				return err
			}
		} else {
			// table form (default)
			if _, err := fmt.Fprintf(w, "<div style=\"margin-top:12px\">"+
				"<form class=\"form\" method=\"post\" action=\"%s\">"+
				"<input type=\"hidden\" name=\"section\" value=\"globals\">"+
				"<input type=\"hidden\" name=\"mode\" value=\"table\">", templ.EscapeString(formAction)); err != nil {
				return err
			}
			if _, err := io.WriteString(w,
				"<div style=\"display:flex;align-items:center;justify-content:space-between;gap:10px;flex-wrap:wrap;margin-bottom:12px\">"+
					"<div class=\"muted\" style=\"font-size:12px\">开关关闭的变量不会保存</div>"+
					"<button class=\"btn\" type=\"button\" id=\"globals_add_row\">添加变量</button>"+
					"</div>"+
					"<table class=\"table\"><thead><tr><th style=\"width:88px\">开关</th><th style=\"width:32%\">Key</th><th>Value</th><th style=\"width:96px\">操作</th></tr></thead><tbody id=\"globals_table_body\" data-next-idx=\""+fmt.Sprint(nextIdx)+"\">"); err != nil {
				return err
			}
			for _, r := range rows {
				ck := ""
				if r.On {
					ck = " checked"
				}
				rowIdx := fmt.Sprint(r.Idx)
				if _, err := io.WriteString(w, "<tr data-idx=\""+templ.EscapeString(rowIdx)+"\">"+
					"<td>"+
					"<input type=\"hidden\" name=\"global_row\" value=\""+templ.EscapeString(rowIdx)+"\">"+
					"<label class=\"toggle\"><input type=\"checkbox\" name=\"global_on_"+templ.EscapeString(rowIdx)+"\" value=\"1\""+ck+">"+
					"<span class=\"slider\"></span></label>"+
					"</td>"+
					"<td><input name=\"global_key_"+templ.EscapeString(rowIdx)+"\" value=\""+templ.EscapeString(r.K)+"\" placeholder=\"NAME\"></td>"+
					"<td><input name=\"global_value_"+templ.EscapeString(rowIdx)+"\" value=\""+templ.EscapeString(r.V)+"\" placeholder=\"value\"></td>"+
					"<td><button class=\"btn small\" type=\"button\" data-action=\"remove\">删除</button></td>"+
					"</tr>"); err != nil {
					return err
				}
			}
			if _, err := io.WriteString(w, "</tbody></table>"+
				"<div class=\"muted\" style=\"font-size:12px;margin-top:8px\">留空 Key 会被忽略；Key 建议使用 <code>[A-Za-z_][A-Za-z0-9_]*</code>。</div>"+
				"<div style=\"display:flex;gap:10px;align-items:center;margin-top:12px\">"+
				"<button class=\"btn primary\" type=\"submit\">保存</button>"+
				"<a class=\"btn\" href=\"/\">返回仪表盘</a>"+
				"</div></form>"); err != nil {
				return err
			}
			if _, err := io.WriteString(w, `<script>
(function() {
  var body = document.getElementById('globals_table_body');
  var add = document.getElementById('globals_add_row');
  if (!body || !add) return;

  function esc(s) {
    return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/"/g, '&quot;');
  }

  function addRow() {
    var idx = body.getAttribute('data-next-idx') || '0';
    var n = parseInt(idx, 10);
    if (isNaN(n)) n = 0;
    body.setAttribute('data-next-idx', String(n + 1));

    var tr = document.createElement('tr');
    tr.setAttribute('data-idx', String(n));
    tr.innerHTML = ''
      + '<td>'
      +   '<input type="hidden" name="global_row" value="' + esc(n) + '">'
      +   '<label class="toggle"><input type="checkbox" name="global_on_' + esc(n) + '" value="1" checked><span class="slider"></span></label>'
      + '</td>'
      + '<td><input name="global_key_' + esc(n) + '" value="" placeholder="NAME"></td>'
      + '<td><input name="global_value_' + esc(n) + '" value="" placeholder="value"></td>'
      + '<td><button class="btn small" type="button" data-action="remove">删除</button></td>';
    body.appendChild(tr);
  }

  add.addEventListener('click', addRow);
  body.addEventListener('click', function(e) {
    var t = e.target;
    if (t && t.getAttribute && t.getAttribute('data-action') === 'remove') {
      e.preventDefault();
      var tr = t.closest('tr');
      if (tr) tr.remove();
    }
  });
})();
</script>`); err != nil {
				return err
			}
			if _, err := io.WriteString(w, "</div>"); err != nil {
				return err
			}
		}

		if _, err := io.WriteString(w, "</section>"); err != nil {
			return err
		}
		return nil
	})
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
