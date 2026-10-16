// These build constraints are copied from webrtc's settingengine.go.
//go:build !js
// +build !js

package webtorrent

import (
	"io"

	"github.com/pion/logging"
	"github.com/pion/webrtc/v4"
)

var s = webrtc.SettingEngine{
	// WebRTC's own logging is dropped.
	LoggerFactory: discardLoggerFactory{},
}

type discardLoggerFactory struct{}

func (discardLoggerFactory) NewLogger(scope string) logging.LeveledLogger {
	return logging.NewDefaultLeveledLoggerForScope(scope, logging.LogLevelInfo, io.Discard)
}
