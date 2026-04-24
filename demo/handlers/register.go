package handlers

import "github.com/fookiejs/fookie/pkg/runtime"

func Register(exec *runtime.Executor) {
	exec.ExternalManager().Register("RunTransferBatch", runTransferBatch)
	exec.ExternalManager().Register("GrowUserbase", growUserbase)
	exec.ExternalManager().Register("RunAtmActivity", runAtmActivity)
}
