// Package example 內嵌本服務的範例設定, 並登記給 `config default`,
// 由它複製到設定目錄 (~/.config/<app>/). .env.example 只列 credential key, 值一律留空.
package example

import (
	_ "embed"

	cfgcmd "github.com/bizshuk/gosdk/cmd/config"
)

//go:embed settings.example.json
var Settings []byte

func init() {
	cfgcmd.MustRegisterDefault("settings.json", Settings)
}
