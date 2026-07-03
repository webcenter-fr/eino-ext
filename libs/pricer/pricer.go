package pricer

type Tokens struct {
	Input     int
	Output    int
	Reasoning int
	Cache     CacheTokens
}

type CacheTokens struct {
	Read  int
	Write int
}

type Pricer interface {
	Cost(model string, tokens Tokens) float64
}
