package card

// dm_actions.go —— 卡片回调 action id 常量（对齐 TS card/dm-cards 的 DM/GS + run-card 的 RC）。
// dispatcher 按 callback value.a 路由。集中定义确保卡片构造与路由用同源 id。

// DM 私聊控制台 action id。
const (
	DMMenu                 = "dm.menu"
	DMNewProject           = "dm.newProject"
	DMNewProjectSubmit     = "dm.newProject.submit"
	DMJoinGroupSubmit      = "dm.joinGroup.submit"
	DMProjects             = "dm.projects"
	DMSettings             = "dm.settings"
	DMDoctor               = "dm.doctor"
	DMReconnect            = "dm.reconnect"
	DMRestart              = "dm.restart"
	DMRestartDo            = "dm.restart.do"
	DMUpdate               = "dm.update"
	DMUpdateDo             = "dm.update.do"
	DMUsage                = "dm.usage"
	DMUsageRefresh         = "dm.usage.refresh"
	DMUsageShare           = "dm.usage.share"
	DMUsageShareDo         = "dm.usage.share.do"
	DMRmConfirm            = "dm.rmConfirm"
	DMRmDo                 = "dm.rmDo"
	DMRmCancel             = "dm.rmCancel"
	DMSetTools             = "dm.set.tools"
	DMSetShowModel         = "dm.set.showModel"
	DMSetWatchdog          = "dm.set.watchdog"
	DMWatchdogCustom       = "dm.set.watchdog.custom"
	DMWatchdogCustomSubmit = "dm.set.watchdog.customSubmit"
	DMSetPending           = "dm.set.pending"
	DMSetConcurrency       = "dm.set.concurrency"
	DMSetProjectsRootDir    = "dm.set.projectsRootDir"
	DMAdmins               = "dm.admins"
	DMAddAdminForm         = "dm.admin.addForm"
	DMAddAdminSubmit       = "dm.admin.addSubmit"
	DMRmAdmin              = "dm.admin.rm"
	DMAllowlist            = "dm.allowlist"
	DMAddAllowedForm       = "dm.allow.addForm"
	DMAddAllowedSubmit     = "dm.allow.addSubmit"
	DMRmAllowed            = "dm.allow.rm"
	DMProjectSettings      = "dm.projectSettings"
	DMProjectTopics        = "dm.projectTopics"
	DMSetNoMentionDm       = "dm.proj.noMention"
	DMSetAutoCompactDm     = "dm.proj.autoCompact"
	DMModelDefault         = "dm.proj.modelDefault"
	DMModelDefaultSubmit   = "dm.proj.modelDefault.submit"
	DMPermission           = "dm.proj.perm"
	DMPermissionSubmit     = "dm.proj.perm.submit"

	// ☕ 咖啡一下（离开接管）：从全局设置卡进入的二级卡片入口。
	DMCoffeeSettings = "dm.coffee.settings"

	// 📝 云文档评论 @bot 全局设置入口 + 后端级联 + 模型/强度表单 + 提示词编辑。
	DMCommentSettings       = "dm.comment.settings"
	DMCommentSetBackend    = "dm.comment.setBackend"
	DMCommentSubmit         = "dm.comment.submit"
	DMCommentEditPrompt     = "dm.comment.editPrompt"
	DMCommentPromptSubmit   = "dm.comment.promptSubmit"
	DMCommentResetPrompt    = "dm.comment.resetPrompt"

	// 🔔 普通群任务结束提醒：四档策略 + 长任务阈值子卡。
	DMSetCompletionReminder          = "dm.set.completionReminder"
	DMCompletionReminderCustom       = "dm.set.completionReminder.custom"
	DMCompletionReminderCustomSubmit = "dm.set.completionReminder.customSubmit"
)

// GS 群内 /settings action id（dm-cards port 时补全）。
const (
	GSSetNoMention       = "gs.noMention"
	GSSetAutoCompact     = "gs.autoCompact"
	GSSettings           = "gs.settings"
	GSModelDefault       = "gs.modelDefault"
	GSModelDefaultSubmit = "gs.modelDefault.submit"
)

// RC 运行卡 action id（⏹ 终止 / 🎯 结束 goal）。对齐 TS RC.stop="run.stop"/RC.endGoal="goal.end"。
const (
	RCStop    = "run.stop"
	RCEndGoal = "goal.end"
)
