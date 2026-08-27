package service

import "errors"

// 公共错误
var (
	ErrOutOfRange         = errors.New("行号超出范围")
	ErrNoData             = errors.New("没有数据")
	ErrProfileNotFound    = errors.New("订阅模板未找到")
	ErrDataSourceNotFound = errors.New("数据源未找到")
)
