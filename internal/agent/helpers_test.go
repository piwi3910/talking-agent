package agent

import "enterprise-ai-demo/internal/tools"

func newHTTPExecutor(url string) tools.HTTPExecutor { return tools.HTTPExecutor{BaseURL: url} }
func request(industry, user, tool string) tools.Request {
	return tools.Request{Industry: industry, UserID: user, Name: tool}
}
