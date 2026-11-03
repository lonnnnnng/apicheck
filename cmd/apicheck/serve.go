package main

import (
	"fmt"
	"net/http"
)

func runServer(listen, staticDir string) error {
	fmt.Printf("API Checker web UI: http://%s\n", listen)
	return http.ListenAndServe(listen, newServerHandler(staticDir))
}
