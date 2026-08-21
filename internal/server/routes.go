package server

import (
	"net/http"
	"strconv"

	"github.com/philsphicas/bgtask/internal/taskservice"
)

// api holds the dependencies shared by every REST route handler and
// registers them onto a mux under /api/v1. It is only constructed (and
// registered) when Options.Expose contains ExposeREST.
type api struct {
	svc  *taskservice.Service
	opts Options
}

func (a *api) register(mux *http.ServeMux) {
	const base = "/api/v1"
	mux.HandleFunc("GET "+base+"/tasks", a.listTasks)
	mux.HandleFunc("POST "+base+"/tasks", a.runTask)
	mux.HandleFunc("GET "+base+"/tasks/{ref}", a.getTask)
	mux.HandleFunc("DELETE "+base+"/tasks/{ref}", a.deleteTask)
	mux.HandleFunc("GET "+base+"/tasks/{ref}/logs", a.taskLogs)
	mux.HandleFunc("POST "+base+"/tasks/{ref}/start", a.startTask)
	mux.HandleFunc("POST "+base+"/tasks/{ref}/stop", a.stopTask)
	mux.HandleFunc("POST "+base+"/tasks/{ref}/restart", a.restartTask)
	mux.HandleFunc("POST "+base+"/tasks/{ref}/rename", a.renameTask)
	mux.HandleFunc("PUT "+base+"/tasks/{ref}/labels", a.setLabels)
	mux.HandleFunc("POST "+base+"/actions/{action}", a.bulkAction)
}

// parseBoolQuery reports whether key is present as a "truthy" query
// parameter: bare (?all), or set to a value strconv.ParseBool accepts
// (true/false/1/0/...). An absent key, or a present-but-unparseable value,
// is treated as false.
func parseBoolQuery(r *http.Request, key string) bool {
	values := r.URL.Query()
	if !values.Has(key) {
		return false
	}
	v := values.Get(key)
	if v == "" {
		return true
	}
	b, err := strconv.ParseBool(v)
	return err == nil && b
}
