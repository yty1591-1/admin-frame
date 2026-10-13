// Package platform 负责进程级装配：配置、日志、HTTP Server、中间件与模块接线。
//
// 本包可被多个 cmd 复用；不得包含具体业务规则。
package platform
