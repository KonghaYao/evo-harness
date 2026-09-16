package migrations

import "embed"

// SQL 含分析表、Harbor 兼容表与索引。由 store 在打开数据库时按文件名顺序执行。
//
//go:embed *.sql
var SQL embed.FS
