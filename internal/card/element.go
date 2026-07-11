package card

// element.go —— 飞书卡片 schema 2.0 element builder 族（对齐 TS card/cards）。
// 纯数据构造：每个 builder 返回 map[string]any（CardElement/CardObject），零飞书 SDK 调用。
// 关键不变量（见 TS 注释）：
//   - schema 2.0（按钮卡走 CardKit 实体只认 2.0）；print_frequency_ms/print_step 必须 {default:N}。
//   - 多控件同行用 column_set（2.0 无 tag:'action'）；callback value 放 behaviors:[{type:'callback',value}]，路由靠 value.a。

// CardElement / CardObject 卡片元素 / 卡片对象（飞书 schema 2.0 JSON）。
type CardElement = map[string]any
type CardObject = map[string]any

// HeaderTemplate 头部底色。
type HeaderTemplate string

const (
	HeaderBlue      HeaderTemplate = "blue"
	HeaderWathet    HeaderTemplate = "wathet"
	HeaderTurquoise HeaderTemplate = "turquoise"
	HeaderGreen     HeaderTemplate = "green"
	HeaderGrey      HeaderTemplate = "grey"
	HeaderRed       HeaderTemplate = "red"
	HeaderOrange    HeaderTemplate = "orange"
)

// NoteColor / PanelBorder / ButtonType 命名颜色与按钮类型。
type NoteColor = string
type PanelBorder = string
type ButtonType = string

const (
	ButtonDefault ButtonType = "default"
	ButtonPrimary ButtonType = "primary"
	ButtonDanger  ButtonType = "danger"
)

// ActionValue 路由 payload（嵌入交互元素的 callback value；a=dispatcher 路由的 action id）。
type ActionValue = map[string]any

// TextTag 头部右侧状态药丸。
type TextTag struct {
	Text  string
	Color string
}

// CardHeader 头部。
type CardHeader struct {
	Title    string
	Template HeaderTemplate // 空 → blue
	Subtitle string
	TextTags []TextTag
}

// CardOpts card() 选项。
type CardOpts struct {
	Header    *CardHeader
	Streaming bool   // 运行卡：开 streaming_mode（answer 元素 typewriter）
	Summary   string // 移动端推送预览
	Forward   *bool  // nil=默认 true；false=禁止转发
	WidthMode string // 卡片宽度：'default'|'compact'|'fill'（PC/iPad 端）
}

// Card 构造一张 schema 2.0 卡片。
func Card(elements []CardElement, opts CardOpts) CardObject {
	config := CardElement{"update_multi": true}
	if opts.Forward != nil && !*opts.Forward {
		config["enable_forward"] = false
	}
	if opts.Streaming {
		config["streaming_mode"] = true
		config["streaming_config"] = CardElement{
			"print_frequency_ms": CardElement{"default": 25},
			"print_step":         CardElement{"default": 6},
			"print_strategy":     "fast",
		}
	}
	if opts.Summary != "" {
		config["summary"] = CardElement{"content": opts.Summary}
	}
	obj := CardObject{
		"schema": "2.0",
		"config": config,
		"body":   CardElement{"elements": elements},
	}
	if opts.WidthMode != "" {
		obj["width_mode"] = opts.WidthMode
	}
	if opts.Header != nil {
		tmpl := string(opts.Header.Template)
		if tmpl == "" {
			tmpl = string(HeaderBlue)
		}
		header := CardElement{
			"template": tmpl,
			"title":    CardElement{"tag": "plain_text", "content": opts.Header.Title},
		}
		if opts.Header.Subtitle != "" {
			header["subtitle"] = CardElement{"tag": "plain_text", "content": opts.Header.Subtitle}
		}
		if len(opts.Header.TextTags) > 0 {
			tags := make([]CardElement, 0, len(opts.Header.TextTags))
			for _, t := range opts.Header.TextTags {
				tags = append(tags, CardElement{
					"tag":   "text_tag",
					"text":  CardElement{"tag": "plain_text", "content": t.Text},
					"color": t.Color,
				})
			}
			header["text_tag_list"] = tags
		}
		obj["header"] = header
	}
	return obj
}

// Md markdown 文本块。
func Md(content string) CardElement {
	return CardElement{"tag": "markdown", "content": content}
}

// MdStream 带 element_id 的 markdown（流式 typewriter 驱动）。
func MdStream(content, elementID string) CardElement {
	return CardElement{"tag": "markdown", "element_id": elementID, "content": content}
}

// Image 图片元素（img_key 来自 im.v1.image.create；markdown ![]() 不渲染）。
func Image(imgKey, alt string) CardElement {
	return CardElement{
		"tag":     "img",
		"img_key": imgKey,
		"alt":     CardElement{"tag": "plain_text", "content": alt},
		"mode":    "fit_horizontal",
		"preview": true,
	}
}

// Note 灰色注脚行（notation size + grey）。
func Note(content string) CardElement {
	return CardElement{"tag": "div", "text": CardElement{"tag": "lark_md", "content": content, "text_size": "notation", "text_color": "grey"}}
}

// ColorNote 彩色注脚行（context gauge green→red / auto-compact 通知）。
func ColorNote(content, color NoteColor) CardElement {
	return CardElement{"tag": "div", "text": CardElement{"tag": "lark_md", "content": content, "text_size": "notation", "text_color": color}}
}

// Hr 分隔线。
func Hr() CardElement { return CardElement{"tag": "hr"} }

// NoteMd 小号 markdown 行（状态/终态注脚）。
func NoteMd(content string) CardElement {
	return CardElement{"tag": "markdown", "content": content, "text_size": "notation"}
}

// CollapsiblePanelOpts 折叠面板选项。
type CollapsiblePanelOpts struct {
	Title    string
	Expanded bool
	Border   PanelBorder
	Body     string // markdown 体（单 markdown 元素）
}

// CollapsiblePanel 折叠面板（reasoning / 工具详情）。
func CollapsiblePanel(opts CollapsiblePanelOpts) CardElement {
	return CardElement{
		"tag":      "collapsible_panel",
		"expanded": opts.Expanded,
		"header": CardElement{
			"title":               CardElement{"tag": "markdown", "content": opts.Title},
			"vertical_align":      "center",
			"icon":                CardElement{"tag": "standard_icon", "token": "down-small-ccm_outlined", "size": "16px 16px"},
			"icon_position":       "follow_text",
			"icon_expanded_angle": -180,
		},
		"border":           CardElement{"color": opts.Border, "corner_radius": "5px"},
		"vertical_spacing": "8px",
		"padding":          "8px 8px 8px 8px",
		"elements":         []CardElement{{"tag": "markdown", "content": opts.Body, "text_size": "notation"}},
	}
}

// CollapsiblePanelEl 折叠面板（体为任意元素列表，支持嵌套面板）。
func CollapsiblePanelEl(title string, expanded bool, border PanelBorder, elements []CardElement) CardElement {
	return CardElement{
		"tag":      "collapsible_panel",
		"expanded": expanded,
		"header": CardElement{
			"title":               CardElement{"tag": "markdown", "content": title},
			"vertical_align":      "center",
			"icon":                CardElement{"tag": "standard_icon", "token": "down-small-ccm_outlined", "size": "16px 16px"},
			"icon_position":       "follow_text",
			"icon_expanded_angle": -180,
		},
		"border":           CardElement{"color": border, "corner_radius": "5px"},
		"vertical_spacing": "8px",
		"padding":          "8px 8px 8px 8px",
		"elements":         elements,
	}
}

// Actions 一行交互控件（2.0 用 column_set flow，一控件一列）。
func Actions(items []CardElement, elementID string) CardElement {
	return actionsRow(items, elementID, "flow", "small", false, "")
}

// ActionsFixed 同 Actions 但控件固定宽度 + large size + 8px 间距。
func ActionsFixed(items []CardElement, width, elementID string) CardElement {
	return actionsRowWithWidth(items, elementID, width)
}

// SplitRow 双列：左控件自然宽 + 右内容填满（button + 注脚）。
func SplitRow(left, right CardElement, elementID string) CardElement {
	out := CardElement{
		"tag":                "column_set",
		"flex_mode":          "none",
		"horizontal_spacing": "medium",
		"columns": []CardElement{
			{"tag": "column", "width": "auto", "vertical_align": "center", "elements": []CardElement{left}},
			{"tag": "column", "width": "weighted", "weight": 1, "vertical_align": "center", "elements": []CardElement{right}},
		},
	}
	if elementID != "" {
		out["element_id"] = elementID
	}
	return out
}

func actionsRow(items []CardElement, elementID, flexMode, spacing string, _ bool, _ string) CardElement {
	columns := make([]CardElement, 0, len(items))
	for _, it := range items {
		columns = append(columns, CardElement{"tag": "column", "width": "auto", "elements": []CardElement{it}})
	}
	out := CardElement{
		"tag":                "column_set",
		"flex_mode":          flexMode,
		"horizontal_spacing": spacing,
		"columns":            columns,
	}
	if elementID != "" {
		out["element_id"] = elementID
	}
	return out
}

func actionsRowWithWidth(items []CardElement, elementID, width string) CardElement {
	columns := make([]CardElement, 0, len(items))
	for _, it := range items {
		// 复制 it 并覆盖 width/size=large。
		col := CardElement{}
		for k, v := range it {
			col[k] = v
		}
		col["width"] = width
		col["size"] = "large"
		columns = append(columns, CardElement{"tag": "column", "width": "auto", "elements": []CardElement{col}})
	}
	out := CardElement{
		"tag":                "column_set",
		"flex_mode":          "flow",
		"horizontal_spacing": "8px",
		"columns":            columns,
	}
	if elementID != "" {
		out["element_id"] = elementID
	}
	return out
}

// Button 回调按钮。
func Button(label string, value ActionValue, btnType ButtonType) CardElement {
	if btnType == "" {
		btnType = ButtonDefault
	}
	return CardElement{
		"tag":       "button",
		"text":      CardElement{"tag": "plain_text", "content": label},
		"type":      btnType,
		"behaviors": []CardElement{{"type": "callback", "value": value}},
	}
}

// LinkButton 开 URL 按钮。
func LinkButton(label, url string, btnType ButtonType, size string) CardElement {
	if btnType == "" {
		btnType = ButtonDefault
	}
	b := CardElement{
		"tag":       "button",
		"text":      CardElement{"tag": "plain_text", "content": label},
		"type":      btnType,
		"behaviors": []CardElement{{"type": "open_url", "default_url": url}},
	}
	if size != "" {
		b["size"] = size
	}
	return b
}

// InputOpts 文本输入选项。
type InputOpts struct {
	Name        string
	Label       string
	Placeholder string
	Value       string
	Required    bool
	// InputType 'text'(单行,默认) | 'multiline_text'(多行文本框,保留换行)。
	InputType string
	// Rows multiline_text 初始可见行数（box 会随内容自动增高）。
	Rows int
	// Width 输入框宽度：'default'(固定窄) | 'fill'(撑满) | 数字(自定义像素,≥100)。
	Width string
	// MaxLength 飞书输入长度上限（1–1000，越界会导致整卡被拒，调用方需自约束）。
	MaxLength int
}

// Input 文本输入。
func Input(opts InputOpts) CardElement {
	e := CardElement{"tag": "input", "name": opts.Name, "required": opts.Required}
	if opts.InputType != "" {
		e["input_type"] = opts.InputType
	}
	if opts.Rows != 0 {
		e["rows"] = opts.Rows
		e["auto_resize"] = true
	}
	if opts.Width != "" {
		e["width"] = opts.Width
	}
	if opts.Label != "" {
		e["label"] = CardElement{"tag": "plain_text", "content": opts.Label}
	}
	if opts.Placeholder != "" {
		e["placeholder"] = CardElement{"tag": "plain_text", "content": opts.Placeholder}
	}
	if opts.Value != "" {
		e["default_value"] = opts.Value
	}
	if opts.MaxLength != 0 {
		e["max_length"] = opts.MaxLength
	}
	return e
}

// Form 表单容器。
func Form(name string, elements []CardElement) CardElement {
	return CardElement{"tag": "form", "name": name, "elements": elements}
}

// SubmitButton 表单提交按钮。
func SubmitButton(label string, value ActionValue, btnType ButtonType, name string) CardElement {
	if btnType == "" {
		btnType = ButtonPrimary
	}
	if name == "" {
		name = "submit"
	}
	return CardElement{
		"tag":              "button",
		"name":             name,
		"text":             CardElement{"tag": "plain_text", "content": label},
		"type":             btnType,
		"form_action_type": "submit",
		"behaviors":        []CardElement{{"type": "callback", "value": value}},
	}
}

// SelectOption 下拉选项。
type SelectOption struct {
	Label string
	Value string
}

// SelectStaticOpts 回调型静态下拉。
type SelectStaticOpts struct {
	ActionID    string
	Placeholder string
	Options     []SelectOption
	Initial     string
}

// SelectStatic 回调型静态下拉（选即触发 callback）。
func SelectStatic(opts SelectStaticOpts) CardElement {
	e := CardElement{
		"tag":         "select_static",
		"placeholder": CardElement{"tag": "plain_text", "content": opts.Placeholder},
		"options":     selectOptions(opts.Options),
		"behaviors":   []CardElement{{"type": "callback", "value": ActionValue{"a": opts.ActionID}}},
	}
	if opts.Initial != "" {
		e["initial_option"] = opts.Initial
	}
	return e
}

// SelectMenu 表单内静态下拉（值经 form_value[name] 收，不独立回调）。
func SelectMenu(name, placeholder string, options []SelectOption, initial string) CardElement {
	e := CardElement{
		"tag":         "select_static",
		"name":        name,
		"placeholder": CardElement{"tag": "plain_text", "content": placeholder},
		"options":     selectOptions(options),
	}
	if initial != "" {
		e["initial_option"] = initial
	}
	return e
}

// MultiSelectMenu 表单内多选下拉。
func MultiSelectMenu(name, placeholder string, options []SelectOption) CardElement {
	return CardElement{
		"tag":         "multi_select_static",
		"name":        name,
		"placeholder": CardElement{"tag": "plain_text", "content": placeholder},
		"options":     selectOptions(options),
	}
}

// SelectPerson 表单内人员选择。
func SelectPerson(name, placeholder string, required bool) CardElement {
	e := CardElement{"tag": "select_person", "name": name, "required": required}
	if placeholder != "" {
		e["placeholder"] = CardElement{"tag": "plain_text", "content": placeholder}
	}
	return e
}

func selectOptions(opts []SelectOption) []CardElement {
	out := make([]CardElement, 0, len(opts))
	for _, o := range opts {
		out = append(out, CardElement{"text": CardElement{"tag": "plain_text", "content": o.Label}, "value": o.Value})
	}
	return out
}
