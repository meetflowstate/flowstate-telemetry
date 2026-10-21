package hooks

import _ "embed"

// CursorHookScript contains the bash hook script for Cursor editor telemetry.
//
//go:embed scripts/flowstate-cursor.sh
var CursorHookScript string

// WindsurfHookScript contains the bash hook script for Windsurf editor telemetry.
//
//go:embed scripts/flowstate-windsurf.sh
var WindsurfHookScript string

// AntigravityHookScript contains the bash hook script for Google Antigravity telemetry.
//
//go:embed scripts/flowstate-antigravity.sh
var AntigravityHookScript string
