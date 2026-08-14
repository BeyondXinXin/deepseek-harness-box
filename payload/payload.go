// Package payload 通过 go:embed 内嵌构建期生成的 payload.zip。
//
// 运行 scripts/build-payload.ps1 会在本目录生成 payload.zip；随后 go build
// 才能成功。payload.zip 内含 node/（Node.js 运行时）与 dsh/（已安装好的 DSH）。
package payload

import _ "embed"

// Zip 是构建期嵌入的完整运行环境压缩包。
//
//go:embed payload.zip
var Zip []byte
