package controller

type Fetcher interface {
	Get(id, path string) error
}
