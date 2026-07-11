package card

import "testing"

func TestCard_Schema2(t *testing.T) {
	c := Card([]CardElement{Md("hi")}, CardOpts{})
	if c["schema"] != "2.0" {
		t.Fatal("schema must be 2.0")
	}
	cfg := c["config"].(CardElement)
	if cfg["update_multi"] != true {
		t.Fatal("update_multi must be true")
	}
	body := c["body"].(CardElement)
	els := body["elements"].([]CardElement)
	if len(els) != 1 || els[0]["tag"] != "markdown" {
		t.Fatalf("body elements wrong: %+v", els)
	}
}

func TestCard_HeaderAndTextTags(t *testing.T) {
	c := Card(nil, CardOpts{
		Header:  &CardHeader{Title: "T", Template: HeaderGreen, TextTags: []TextTag{{Text: "v1", Color: "blue"}}},
		Summary: "preview",
	})
	h := c["header"].(CardElement)
	if h["template"] != "green" {
		t.Fatalf("template=%v want green", h["template"])
	}
	if h["title"].(CardElement)["content"] != "T" {
		t.Fatal("title wrong")
	}
	tags := h["text_tag_list"].([]CardElement)
	if len(tags) != 1 || tags[0]["color"] != "blue" {
		t.Fatalf("text tags wrong: %+v", tags)
	}
	cfg := c["config"].(CardElement)
	if cfg["summary"].(CardElement)["content"] != "preview" {
		t.Fatal("summary wrong")
	}
}

func TestCard_StreamingConfigUsesDefaultObjects(t *testing.T) {
	// print_frequency_ms / print_step 必须是 {default:N}（裸 int 会反序列化失败）。
	c := Card(nil, CardOpts{Streaming: true})
	cfg := c["config"].(CardElement)
	sc := cfg["streaming_config"].(CardElement)
	if sc["print_frequency_ms"].(CardElement)["default"] != 25 {
		t.Fatal("print_frequency_ms must be {default:25}")
	}
	if sc["print_step"].(CardElement)["default"] != 6 {
		t.Fatal("print_step must be {default:6}")
	}
}

func TestCard_DisableForward(t *testing.T) {
	f := false
	c := Card(nil, CardOpts{Forward: &f})
	if c["config"].(CardElement)["enable_forward"] != false {
		t.Fatal("enable_forward should be false")
	}
	// 默认（nil）不写 enable_forward。
	c2 := Card(nil, CardOpts{})
	if _, ok := c2["config"].(CardElement)["enable_forward"]; ok {
		t.Fatal("nil forward should not set enable_forward")
	}
}

func TestMdStream(t *testing.T) {
	e := MdStream("body", "answer")
	if e["element_id"] != "answer" || e["content"] != "body" {
		t.Fatalf("mdStream wrong: %+v", e)
	}
}

func TestButton_CallbackValue(t *testing.T) {
	b := Button("OK", ActionValue{"a": "RC.stop"}, ButtonPrimary)
	if b["type"] != "primary" {
		t.Fatal("type")
	}
	behaviors := b["behaviors"].([]CardElement)
	if behaviors[0]["type"] != "callback" {
		t.Fatal("behavior must be callback")
	}
	if behaviors[0]["value"].(ActionValue)["a"] != "RC.stop" {
		t.Fatal("value.a must route action")
	}
}

func TestActions_ColumnSet(t *testing.T) {
	row := Actions([]CardElement{Button("a", ActionValue{"a": "x"}, "")}, "controls")
	if row["tag"] != "column_set" {
		t.Fatal("actions must be column_set in schema 2.0")
	}
	if row["element_id"] != "controls" {
		t.Fatal("element_id")
	}
}

func TestCollapsiblePanel(t *testing.T) {
	p := CollapsiblePanel(CollapsiblePanelOpts{Title: "思考", Expanded: false, Border: "grey", Body: "detail"})
	if p["tag"] != "collapsible_panel" || p["expanded"] != false {
		t.Fatalf("collapsible panel wrong: %+v", p)
	}
	border := p["border"].(CardElement)
	if border["color"] != "grey" {
		t.Fatal("border color")
	}
}

func TestSelectStatic_Callback(t *testing.T) {
	s := SelectStatic(SelectStaticOpts{
		ActionID:    "MC.model",
		Placeholder: "选模型",
		Options:     []SelectOption{{Label: "GPT-5", Value: "gpt5"}},
	})
	if s["tag"] != "select_static" {
		t.Fatal("tag")
	}
	behaviors := s["behaviors"].([]CardElement)
	if behaviors[0]["value"].(ActionValue)["a"] != "MC.model" {
		t.Fatal("selectStatic must carry callback value.a")
	}
	opts := s["options"].([]CardElement)
	if opts[0]["value"] != "gpt5" {
		t.Fatal("option value")
	}
}

func TestFormAndSubmit(t *testing.T) {
	f := Form("myform", []CardElement{Input(InputOpts{Name: "x"})})
	if f["tag"] != "form" || f["name"] != "myform" {
		t.Fatalf("form wrong: %+v", f)
	}
	sb := SubmitButton("提交", ActionValue{"a": "submit"}, "", "")
	if sb["form_action_type"] != "submit" {
		t.Fatal("submit button must have form_action_type=submit")
	}
	if sb["type"] != "primary" {
		t.Fatal("submit button default type=primary")
	}
}

func TestImage(t *testing.T) {
	img := Image("img_key_x", "alt")
	if img["img_key"] != "img_key_x" || img["mode"] != "fit_horizontal" || img["preview"] != true {
		t.Fatalf("image wrong: %+v", img)
	}
}
