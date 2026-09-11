package types

import "strings"

// AnswerQuery keeps the current page and learning plan in the user's message.
// A search rewrite may omit that context; retrieval still uses RewriteQuery.
// This envelope is user-provided data, not trusted system instructions.
func (c *ChatManage) AnswerQuery() string {
	if strings.HasPrefix(c.Query, "[Host context]\n") && strings.Contains(c.Query, "\n[/Host context]\n\n") {
		return c.Query
	}
	if rewritten := strings.TrimSpace(c.RewriteQuery); rewritten != "" {
		return rewritten
	}
	return c.Query
}
