package integrationtools

// Волна 121 (срез Б): единый парсер имён MCP-тулов.

import "testing"

func TestParseMCPToolName(t *testing.T) {
	servers := []string{"github", "github-personal", "notion"}
	cases := []struct {
		name            string
		wantSrv, wantTl string
		wantOK          bool
	}{
		{"mcp_github-personal_list_repositories", "github-personal", "list_repositories", true},
		{"mcp_github_search_repositories", "github", "search_repositories", true},
		{"mcp_notion_fetch", "notion", "fetch", true},
		{"mcp_unknownserver_fetch", "", "", false},
		{"search_memory", "", "", false},
		{"mcp_github", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, tl, ok := ParseMCPToolName(tc.name, servers)
			if ok != tc.wantOK || srv != tc.wantSrv || tl != tc.wantTl {
				t.Errorf("ParseMCPToolName(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tc.name, srv, tl, ok, tc.wantSrv, tc.wantTl, tc.wantOK)
			}
		})
	}
}
