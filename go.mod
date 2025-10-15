module openlist-lanzou-plugin

go 1.24.0

require (
	github.com/OpenListTeam/go-wasi-http v0.0.0-20251015142022-5647e49e373d
	github.com/OpenListTeam/openlist-wasi-plugin-driver v0.0.0-20251015133414-5b50219c1270
	github.com/tidwall/gjson v1.18.0
	go.bytecodealliance.org/cm v0.3.0
	golang.org/x/sync v0.17.0
	resty.dev/v3 v3.0.0-00010101000000-000000000000
)

require (
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.0 // indirect
)

replace resty.dev/v3 => github.com/OpenListTeam/resty-tinygo/v3 v3.0.0-20251013065911-8fff8b24c719
