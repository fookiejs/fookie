package main

// Demo external handlers wired at startup when the demo schema is used.
// The handlers are registered unconditionally — if the schema doesn't declare
// the corresponding externals they are simply never called.

import (
	"github.com/fookiejs/fookie/demo/handlers"
	"github.com/fookiejs/fookie/pkg/runtime"
)

func registerDemoHandlers(exec *runtime.Executor) {
	handlers.Register(exec)
}
