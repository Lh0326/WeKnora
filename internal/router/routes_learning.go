package router

import (
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/gin-gonic/gin"
)

// RegisterLearningRoutes mounts the learning tab's API. The shape follows
// routes_memory.go exactly: Viewer is the only role gate, an API key must
// be full-access, and there is no subject parameter anywhere in the paths
// — the subject is always the caller's principal, so these routes can
// only read or write the caller's own mastery. KB-scoped routes add the
// same KB access guard the wiki read routes use.
func RegisterLearningRoutes(r *gin.RouterGroup, learningHandler *handler.LearningHandler, g *rbacGuards) {
	if learningHandler == nil {
		return // lite deployments without the service
	}

	learningGroup := g.apiKeyGroup(r.Group("/learning", g.Viewer()), apiKeyFullAccess())
	learningGroup.GET("/export", learningHandler.ExportProfile)
	learningGroup.DELETE("/profile", learningHandler.DeleteProfile)
	learningGroup.GET("/settings", learningHandler.GetSettings)
	learningGroup.PUT("/settings", learningHandler.UpdateSettings)

	kbGroup := g.apiKeyGroup(r.Group("/learning/kb/:kb_id", g.Viewer()), apiKeyFullAccess())
	kbGroup.GET("/progress", g.KBAccessRead("kb_id"), learningHandler.GetProgress)
	kbGroup.GET("/map", g.KBAccessRead("kb_id"), learningHandler.ListMastery)
	kbGroup.GET("/recommend", g.KBAccessRead("kb_id"), learningHandler.Recommend)
	// 模块分区视图：每区自己的"1/2" + 全量节点 + 先修关系线。
	kbGroup.GET("/zone-map", g.KBAccessRead("kb_id"), learningHandler.ZoneMap)
	kbGroup.GET("/assessment-freeze", g.KBAccessRead("kb_id"), learningHandler.FreezeAssessment)
	kbGroup.GET("/objectives", g.KBAccessRead("kb_id"), learningHandler.ObjectiveView)
	kbGroup.GET("/components", g.KBAccessRead("kb_id"), learningHandler.ComponentView)
	kbGroup.POST("/components/action", g.KBAccessRead("kb_id"), learningHandler.ComponentAction)
	kbGroup.POST("/components/import", g.KBAccessWrite("kb_id"), learningHandler.ImportComponents)
	kbGroup.POST("/components/draft", g.KBAccessWrite("kb_id"), learningHandler.DraftComponents)
	kbGroup.POST("/path", g.KBAccessRead("kb_id"), learningHandler.ShortPath)
	kbGroup.GET("/plan-preferences", g.KBAccessRead("kb_id"), learningHandler.GetPlanPreferences)
	kbGroup.PUT("/plan-preferences", g.KBAccessRead("kb_id"), learningHandler.UpdatePlanPreferences)
	kbGroup.POST("/recall", g.KBAccessRead("kb_id"), learningHandler.UpdateReviewSchedule)
	kbGroup.GET("/objective-progress", g.KBAccessRead("kb_id"), learningHandler.ObjectiveProgress)
	kbGroup.POST("/objectives/:objective_id/review", g.KBAccessWrite("kb_id"), learningHandler.ReviewObjective)
	kbGroup.POST("/quiz/:item_id/review", g.KBAccessWrite("kb_id"), learningHandler.ReviewQuizItem)
	kbGroup.GET("/tasks", g.KBAccessRead("kb_id"), learningHandler.TakeTask)
	kbGroup.POST("/tasks/:task_id/review", g.KBAccessWrite("kb_id"), learningHandler.ReviewTask)
	kbGroup.POST("/tasks/:task_id/answer", g.KBAccessRead("kb_id"), learningHandler.SubmitTaskAnswer)
	// Passive decay channel: read-time derived, never persisted, kept
	// apart from the action timeline so passive volume cannot flood it.
	kbGroup.GET("/changes", g.KBAccessRead("kb_id"), learningHandler.PassiveChanges)
	kbGroup.GET("/quiz", g.KBAccessRead("kb_id"), learningHandler.TakeQuiz)
	kbGroup.POST("/quiz/:item_id/answer", g.KBAccessRead("kb_id"), learningHandler.SubmitAnswer)
	// Reading a wiki page is a deliberate low-trust touch (§3.3.6 signal).
	kbGroup.POST("/node-state", g.KBAccessRead("kb_id"), learningHandler.SetNodeState)
	kbGroup.POST("/read", g.KBAccessRead("kb_id"), learningHandler.RecordRead)
	kbGroup.POST("/self-assess", g.KBAccessRead("kb_id"), learningHandler.SelfAssess)
	// Standing "已掌握，不再推荐" declaration (and its revocation).
	kbGroup.POST("/skip", g.KBAccessRead("kb_id"), learningHandler.RecordSkip)
	kbGroup.GET("/timeline", g.KBAccessRead("kb_id"), learningHandler.Timeline)

}
