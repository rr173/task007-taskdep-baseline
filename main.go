package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

func main() {
	smoke := flag.Bool("smoke-test", false, "run a built-in self-test and exit")
	addr := flag.String("addr", ":8080", "HTTP listen address")
	flag.Parse()

	if *smoke {
		if err := runSmokeTest(); err != nil {
			fmt.Fprintln(os.Stderr, "smoke-test FAILED:", err)
			os.Exit(1)
		}
		fmt.Println("smoke-test PASSED")
		return
	}

	srv := NewService()
	mux := buildMux(srv)
	log.Printf("taskdep listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

func buildMux(srv *Service) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /tasks", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name      string   `json:"name"`
			DependsOn []string `json:"dependsOn"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, badRequest("invalid JSON body: %v", err))
			return
		}
		t, err := srv.Create(req.Name, req.DependsOn)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, t)
	})

	mux.HandleFunc("GET /tasks", func(w http.ResponseWriter, r *http.Request) {
		filter := Status(r.URL.Query().Get("status"))
		if filter != "" && filter != StatusPending && filter != StatusReady && filter != StatusDone {
			writeError(w, badRequest("invalid status filter %q", filter))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tasks": srv.List(filter)})
	})

	mux.HandleFunc("GET /tasks/{name}", func(w http.ResponseWriter, r *http.Request) {
		t, err := srv.Get(r.PathValue("name"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, t)
	})

	mux.HandleFunc("POST /tasks/{name}/complete", func(w http.ResponseWriter, r *http.Request) {
		t, err := srv.Complete(r.PathValue("name"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, t)
	})

	mux.HandleFunc("GET /order", func(w http.ResponseWriter, r *http.Request) {
		order, err := srv.Order()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"order": order})
	})

	return mux
}

func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return errors.New("request body is required")
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("request body is required")
		}
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	switch e := err.(type) {
	case *cycleErr:
		writeJSON(w, http.StatusConflict, map[string]any{"error": e.Error(), "cycle": e.path})
	case *missingErr:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": e.Error(), "missing": e.names})
	case *statusErr:
		writeJSON(w, e.code, map[string]any{"error": e.msg})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
	}
}
