module github.com/mj41/stackchan-pet

go 1.25

require (
	github.com/gorilla/websocket v1.5.3
	github.com/mj41/stackchan-server v0.0.0
)

// Until stackchan-server is pushed with the public wire package.
replace github.com/mj41/stackchan-server => ../stackchan-server
