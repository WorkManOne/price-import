package main

import (
	"log"
	"net/http"
)

func main() {
	// пока просто заглушка чтоб было что запускать
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	log.Println("listen :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
