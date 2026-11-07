module github.com/choigonyok/jarvis/search-svc

go 1.25.0

require (
	codeberg.org/readeck/go-readability/v2 v2.1.3
	golang.org/x/image v0.45.0
	golang.org/x/net v0.58.0
	golang.org/x/text v0.41.0
)

require github.com/stretchr/testify v1.11.1 // indirect

require (
	github.com/andybalholm/cascadia v1.3.3 // indirect
	github.com/choigonyok/jarvis/gocommon v0.0.0
	github.com/go-shiori/dom v0.0.0-20230515143342-73569d674e1c // indirect
	github.com/gogs/chardet v0.0.0-20211120154057-b7413eaefb8f // indirect
	github.com/itlightning/dateparse v0.2.1 // indirect
)

replace github.com/choigonyok/jarvis/gocommon => ../gocommon
