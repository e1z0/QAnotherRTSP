package main

//go:generate miqt-rcc -Input "./src/resources.qrc" -OutputGo "resources.qrc.go" -OutputRcc "resources.qrc.rcc"

import (
	"embed"

	"github.com/mappu/miqt/qt"
)

//go:embed resources.qrc.rcc
var _resourceRcc []byte

func init() {
	_ = embed.FS{}
	qt.QResource_RegisterResourceWithRccData(&_resourceRcc[0])
}
