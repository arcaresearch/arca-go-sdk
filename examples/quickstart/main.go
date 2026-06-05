// Command quickstart demonstrates the Arca Go SDK: create two wallets, fund
// one, transfer between them, then stream balance changes.
//
// Run with ARCA_API_KEY and ARCA_REALM set:
//
//	ARCA_API_KEY=arca_... ARCA_REALM=my-realm go run ./examples/quickstart
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	arca "github.com/arca-network/arca-go-sdk"
)

func main() {
	apiKey := os.Getenv("ARCA_API_KEY")
	realm := os.Getenv("ARCA_REALM")
	if apiKey == "" || realm == "" {
		log.Fatal("set ARCA_API_KEY and ARCA_REALM")
	}

	ctx := context.Background()
	client, err := arca.New(arca.Config{
		APIKey:  apiKey,
		Realm:   realm,
		BaseURL: envOr("ARCA_BASE_URL", arca.DefaultBaseURL),
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Dispose()

	if err := client.Ready(ctx); err != nil {
		log.Fatal(err)
	}

	for _, ref := range []string{"/users/alice/wallet", "/users/bob/wallet"} {
		if _, err := client.EnsureDenominatedArca(ctx, arca.EnsureDenominatedArcaOptions{Ref: ref}).Wait(ctx); err != nil {
			log.Fatalf("ensure %s: %v", ref, err)
		}
	}

	if _, err := client.FundAccount(ctx, arca.FundAccountOptions{ArcaRef: "/users/alice/wallet", Amount: "1000"}).Wait(ctx); err != nil {
		log.Fatalf("fund: %v", err)
	}

	// Stream balance changes under /users while the transfer settles.
	stream, err := client.WatchBalances(ctx, "/users")
	if err != nil {
		log.Fatalf("watch: %v", err)
	}
	defer stream.Close()
	stream.OnUpdate(func(u arca.BalanceUpdate) {
		fmt.Printf("balance changed: %s\n", u.EntityPath)
	})

	nonce, err := client.Nonce(ctx, "/op/transfer/alice-to-bob/001")
	if err != nil {
		log.Fatal(err)
	}
	if _, err := client.Transfer(ctx, arca.TransferOptions{
		Path:   nonce.Path,
		From:   "/users/alice/wallet",
		To:     "/users/bob/wallet",
		Amount: "50",
	}).Wait(ctx); err != nil {
		log.Fatalf("transfer: %v", err)
	}

	bob, err := client.GetBalancesByPath(ctx, "/users/bob/wallet")
	if err != nil {
		log.Fatal(err)
	}
	if len(bob) > 0 {
		fmt.Println("Bob settled balance:", bob[0].Settled)
	}

	time.Sleep(500 * time.Millisecond) // let any final balance event arrive
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
