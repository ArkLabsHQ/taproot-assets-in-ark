package main

import (
	"encoding/hex"
	"fmt"
	"log"

	"taponark"

	"github.com/btcsuite/btcd/btcec/v2"
)

func main() {
	userPrivKeyBytes, _ := hex.DecodeString("1c3a1d6e1a6c8f2f3d7b4e1c3a1d6e1a6c8f2f3d7b4e1c3a1d6e1a6c8f2f3d7b")
	userPrivKey, _ := btcec.PrivKeyFromBytes(userPrivKeyBytes)
	userPubKey := userPrivKey.PubKey()

	// Define a static private key (must be 32 bytes)
	serverPrivKeyBytes, _ := hex.DecodeString("1c3a1d6e1a6c8f2f3d7b4e1c3a1d6e1a6c8f2f3d7b4e1c3a1d6e1a6c8f2f3d7c")
	serverPrivKey, _ := btcec.PrivKeyFromBytes(serverPrivKeyBytes)
	serverPubKey := serverPrivKey.PubKey()

	currentBlockHeight := 300
	lockBlockHeight := currentBlockHeight + 4320

	assetId, _ := hex.DecodeString("8e781dedad7dfdccc0e5f05dea48c0245766952c44246a752e13d19df7affa34")
	ammt := 1

	arkScript, err := taponark.CreateBoardingArkScript(userPubKey, serverPubKey, uint32(lockBlockHeight))
	if err != nil {
		log.Fatal(err)
	}

	client := taponark.InitTapClient()
	addr, err := client.GetBoardingAddress(arkScript.Branch, assetId, uint64(ammt))

	if err != nil {
		log.Fatal("error", err)
	}
	fmt.Printf("%+v\n", addr.TapscriptSibling)

}
