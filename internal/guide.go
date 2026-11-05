package internal

import (
	_ "embed"
	"fmt"
	"net/http"
	"strings"
)

//go:embed embedded/agent-guide.txt
var agentGuideTemplate string

func publicGuidePath(path string) bool {
	return path == "/" || path == "/llm.txt" || path == "/llms.txt" || path == "/skill.md"
}

func mountAgentGuide(mux *http.ServeMux, publicURL string) {
	base := strings.TrimRight(publicURL, "/")
	if base == "" {
		base = "<server-url>"
	}
	guide := fmt.Sprintf(agentGuideTemplate, base)

	for _, path := range []string{"/llm.txt", "/llms.txt"} {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = fmt.Fprint(w, guide)
		})
	}
	mux.HandleFunc("GET /skill.md", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write(SkillContent)
	})
}
