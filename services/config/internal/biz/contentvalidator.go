package biz

// ContentTarget 只定位一项 Config Center 内容，不携带配置值。
type ContentTarget struct {
	Namespace   string
	Environment string
	Key         string
}

// ContentValidator 在持久化前校验已登记的配置结构。
// 实现返回的错误只能包含位置，不能包含配置值。
type ContentValidator interface {
	Validate(target ContentTarget, format ConfigFormat, value string) error
}
