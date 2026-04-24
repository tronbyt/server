package config

import (
	"context"
	"errors"
)

type ctxKey uint8

const configKey ctxKey = iota

func NewContext(ctx context.Context, conf *Settings) context.Context {
	return context.WithValue(ctx, configKey, conf)
}

var ErrNoContext = errors.New("no config found in context")

func FromContext(ctx context.Context) (*Settings, error) {
	conf, ok := ctx.Value(configKey).(*Settings)
	if !ok {
		return nil, ErrNoContext
	}
	return conf, nil
}
