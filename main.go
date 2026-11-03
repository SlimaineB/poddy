package main

import (
	"context"
	"log"
	"os"

	"poddy/internal/app"
)

func main() {
	if err := app.Run(context.Background(), os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
