package main

import (
	"context"
	"github.com/tesserix/onboarding-service/api/internal/api"
	"log"
	"os"
)

func main() {
	write := api.WriteMCPContract
	if len(os.Args) == 2 && os.Args[1] == "--registry" {
		write = api.WriteMCPRegistry
	}
	if err := write(context.Background(), os.Stdout); err != nil {
		log.Fatal(err)
	}
}
