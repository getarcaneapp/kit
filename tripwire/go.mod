module kit.getarcane.app/tripwire

go 1.27

replace kit.getarcane.app => ../kit

require (
	github.com/fsnotify/fsnotify v1.10.1
	kit.getarcane.app v0.4.4
)

require golang.org/x/sys v0.48.0 // indirect
