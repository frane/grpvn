// A test fixture for the standalone desktop's loopback interface.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/frane/grpvn/uihost"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	server, err := uihost.Start(ctx, "desktop-person", "")
	if err != nil {
		log.Fatal(err)
	}
	defer server.Close()
	fmt.Println(server.URL)
	<-ctx.Done()
}
