module kit.getarcane.app/tripwire

go 1.27

replace kit.getarcane.app => ../kit

require (
	github.com/fsnotify/fsnotify v1.10.1
	kit.getarcane.app v0.7.1
)

require golang.org/x/sys v0.50.0 // indirect
