package main

import (
	"github.com/fookiejs/fookie/demo/handlers"
	"github.com/fookiejs/fookie/pkg/runtime"
)

func registerDemoHandlers(exec *runtime.Executor) {
	handlers.Register(exec)
}
