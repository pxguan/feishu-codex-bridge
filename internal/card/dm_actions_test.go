package card

import "testing"

func TestActionIDs_NonEmptyAndDistinct(t *testing.T) {
	// 全部 action id 必须非空且互异（路由契约）。
	ids := []string{
		DMMenu, DMNewProject, DMNewProjectSubmit, DMJoinGroupSubmit, DMProjects,
		DMSettings, DMDoctor, DMReconnect, DMUpdate, DMUpdateDo,
		DMUsage, DMUsageRefresh, DMUsageShare, DMUsageShareDo,
		DMRmConfirm, DMRmDo, DMRmCancel,
		DMSetTools, DMSetShowModel, DMSetWatchdog, DMWatchdogCustom, DMWatchdogCustomSubmit,
		DMSetPending, DMSetConcurrency,
		DMAdmins, DMAddAdminForm, DMAddAdminSubmit, DMRmAdmin,
		DMAllowlist, DMAddAllowedForm, DMAddAllowedSubmit, DMRmAllowed,
		DMProjectSettings, DMProjectTopics,
		DMSetNoMentionDm, DMSetAutoCompactDm,
		DMModelDefault, DMModelDefaultSubmit, DMPermission, DMPermissionSubmit,
		GSSetNoMention, GSSetAutoCompact, GSSettings,
		RCStop, RCEndGoal,
		MCModel, MCEffort, RESPick,
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" {
			t.Fatal("action id must not be empty")
		}
		if seen[id] {
			t.Fatalf("duplicate action id: %q", id)
		}
		seen[id] = true
	}
}
