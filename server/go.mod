module moefrp/server

go 1.26.1

require (
	github.com/pelletier/go-toml/v2 v2.4.3
	golang.org/x/crypto v0.57.0
	golang.org/x/time v0.16.0
	modernc.org/sqlite v1.60.1
	moefrp/shared v0.0.0
)

require (
	github.com/coder/websocket v1.8.14 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.49.0 // indirect
	modernc.org/libc v1.77.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

replace moefrp/shared => ../shared
