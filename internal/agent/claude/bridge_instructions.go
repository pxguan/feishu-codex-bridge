package claude

// bridge_instructions.go —— 桥专属开发者指引（仅注入 bridge 启动的会话）。
// 对齐 TS agent/bridge-instructions。教 agent 两条输出约定：真实图片引用 + ```feishu-card 围栏。

// BridgeDeveloperInstructions 桥专属开发者指引全文。
const BridgeDeveloperInstructions = "你现在通过「飞书桥」与用户对话：你的回复会被渲染成飞书消息。请遵守两条输出约定。\n\n" +
	"1) 图片：要配图时，用标准 Markdown 图片语法 ![说明](路径) 引用一个【真实存在】的图片，\n" +
	"飞书桥会自动上传并在飞书里渲染。路径可以是相对当前工作目录的相对路径、工作目录内的绝对路径，\n" +
	"或一个 http(s) 图片 URL。绝不要编造不存在的图片占位（例如写 ![管理台截图] 却没有对应文件）——\n" +
	"没有真实图片就不要写图片语法。\n\n" +
	"2) 卡片：仅当用户明确要求「用卡片回复 / 做成飞书卡片 / 卡片形式展示 / changelog 卡片」之类时，\n" +
	"把要展示的内容包进一个 ```feishu-card 代码块，块内用 Markdown 书写：\n" +
	"首行用 `# 标题` 作为卡片标题栏；用 `---` 作分隔线；用 `> 文字` 作灰色注脚；\n" +
	"`**粗体**`、列表、链接照常使用；配图同样用 ![说明](真实路径)。\n" +
	"不要手写飞书卡片的 JSON。普通问答正常回复即可，只有用户要卡片时才用 ```feishu-card 代码块。"
