package worker

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoEnvironmentDumping verifies that the worker and server module code
// does NOT contain os.Environ() calls that would dump all environment variables
// (including secrets) to the logs.
func TestNoEnvironmentDumping(t *testing.T) {
	filesToCheck := []string{
		"module.go",
		"../server/module.go",
	}

	for _, relPath := range filesToCheck {
		absPath, err := filepath.Abs(relPath)
		require.NoError(t, err)

		content, err := os.ReadFile(absPath)
		require.NoError(t, err, "reading %s", relPath)

		assert.NotContains(t, string(content), "os.Environ()",
			"file %s should not call os.Environ() — this leaks secrets to logs", relPath)
	}
}

// TestNoConfigSecretInLogStatements verifies that log statements in module.go
// do not use %+v or %v formatting which would expose secrets in Config objects.
func TestNoConfigSecretInLogStatements(t *testing.T) {
	content, err := os.ReadFile("module.go")
	require.NoError(t, err)

	src := string(content)

	// Check common patterns that would leak secrets via struct formatting
	dangerousPatterns := []string{
		`"%+v", cfg`,
		`"%+v", c`,
		`"%+v", config`,
		`"%+v", conf`,
		`"%v", cfg`,
		`"%v", c `,
		`"%v", config`,
		`"%v", conf`,
		`found one config: %+v`,
		`found one config: %v`,
	}

	for _, pattern := range dangerousPatterns {
		assert.NotContains(t, src, pattern,
			"module.go should not log config structs with %%+v or %%v — this exposes secrets. Found: %s", pattern)
	}
}

// TestNoEventPayloadInSpanAttributes verifies that the event payload is no longer
// stored as a span attribute, which could leak sensitive data to the tracing backend.
func TestNoEventPayloadInSpanAttributes(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "module.go", nil, parser.AllErrors)
	require.NoError(t, err)

	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if strings.Contains(lit.Value, "event-payload") {
				found = true
			}
		}
		return true
	})

	assert.False(t, found,
		"module.go should not store event-payload as a span attribute — it can contain sensitive data")
}

// TestDocument_PercentPlusVLeaksSecrets documents that fmt.Sprintf with %+v
// on any struct containing a Secret field exposes the secret in plaintext.
// This is why config objects must never be logged with %+v or %v.
func TestDocument_PercentPlusVLeaksSecrets(t *testing.T) {
	type configLike struct {
		Endpoint string
		Secret   string
		ID       string
	}

	cfg := configLike{
		Endpoint: "https://example.com",
		Secret:   "not-a-real-secret",
		ID:       "cfg-123",
	}

	output := fmt.Sprintf("%+v", cfg)
	assert.Contains(t, output, cfg.Secret,
		"this confirms %%+v leaks secrets — never log config structs this way")
}
