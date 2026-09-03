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
	// Passive decay channel: read-time derived, never persisted, kept
	// apart from the action timeline so passive volume cannot flood it.
	kbGroup.GET("/changes", g.KBAccessRead("kb_id"), learningHandler.PassiveChanges)
	kbGroup.GET("/quiz", g.KBAccessRead("kb_id"), learningHandler.TakeQuiz)
	kbGroup.POST("/quiz/:item_id/answer", g.KBAccessRead("kb_id"), learningHandler.SubmitAnswer)
	// Reading a wiki page is a deliberate low-trust touch (§3.3.6 signal).
	kbGroup.POST("/read", g.KBAccessRead("kb_id"), learningHandler.RecordRead)
	kbGroup.POST("/self-assess", g.KBAccessRead("kb_id"), learningHandler.SelfAssess)
	kbGroup.GET("/timeline", g.KBAccessRead("kb_id"), learningHandler.Timeline)
}
