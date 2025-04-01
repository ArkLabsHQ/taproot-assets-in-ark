# Ark-Taproot-Asset PoC 🔗  
*A Proof of Concept for Integrating Taproot Assets with the Ark Protocol*

---

## 🧠 Overview

**Ark-Taproot PoC** is an proof of concept CLI App that demonstrates how **Taproot Assets** can be integrated into the **Ark Protocol**. The project uses a local Docker network (configured externally) to simulate interactions with a Taproot Assets daemon (`tapd`) and a Bitcoin node. The primary interface is an interactive REPL where you can issue commands to deposit btc, mint assets,  onboard asset and btc, view asset and btc balance, amongst others.

This PoC is intended for research and prototyping purposes only.

---

## 📦 Getting Started

1. **Clone the Repository:**

   ```bash
   git clone https://github.com/yourusername/ark-taproot-poc.git
   cd ark-taproot-poc
   ```

2. **Configure Network And Run :**

    There are three available networks that can be chosen from to run the POC. **Regtest**, **Signet** and **MutinyNet**.


    Before launching the network, you may need to adjust the connection settings for your Bitcoin client if custom bitcoin node is used. These settings are defined in a YAML file `cmd/config-{network}-yaml`. Open the file in your favorite text editor and modify the properties as needed:

    ```yaml
    bitcoin_client:
      host: "bitcoind.mutinynet.arkade.sh"    # Replace with your Bitcoin node's hostname or IP
      port: "18443"                           # Ensure this is the correct port for your setup
      user: "ceiwHEbqWI83"                    # Update with your RPC username
      password: "DwubwWsoo3"                  # Update with your RPC password

    signet_challenge: 512102f7561d208dd9ae99bf497273e16f389bdbd6c4742ddb8e6b216e64fa2928ad8f51ae
                                              # Signet challenge value; modify if Signet network is used
    ```


   ```bash
   cd cmd
   docker compose -f ./docker-compose-regtest.yml up
   ```

   This network should start:
   - 1 Onboarding User `lnd` and `tapd` node.
   - 1 Exit User `lnd` and `tapd` node.
   - 1 Server `lnd` and `tapd` node.



3. **Launch the REPL:**

   Start the interactive REPL by running:

   ```bash
   cd cmd
   go run . -network {{network}}
   ```

   Once started, you will see a prompt where you can enter commands interactively.

---

##  Ark Tree Construction

```mermaid
flowchart TD
subgraph round_transaction
direction LR
A[Asset  40 tokens] ---> RoundRoot[40 tokens + 100_000 sats]
B[Btc 100_000 sats] ---> RoundRoot
end

subgraph intermediate_txn
RoundRoot ---> RoundRoot_AS_INPUT[Signature EXIT_USER + SERVER]
RoundRoot_AS_INPUT ---> Branch1[Branch1 20 Tokens + 50_000 sats]
RoundRoot_AS_INPUT ---> Branch2[Branch2 20 Tokens + 50_000 sats]
end

subgraph exit_txn2
direction TB
Branch2 ---> Branch2_AS_INPUT[Signature EXIT_USER + SERVER]
Branch2_AS_INPUT ---> Leaf3[Asset 20 tokens]
Branch2_AS_INPUT ---> Leaf4[Btc 50_000 sats]
end

subgraph exit_txn1
direction TB
Branch1 ---> Branch1_AS_INPUT[Signature EXIT_USER + SERVER]
Branch1_AS_INPUT ---> Leaf1[Asset 20 tokens]
Branch1_AS_INPUT ---> Leaf2[Btc 50_000 sats]
end

```

 #### Things To Consider:
  - All leaves output goes to the Exit User both token and bitcoin
  - Both Asset and Bitcoin are split equally between transaction outputs
  - Fees are excluded from Transaction flow, but a fee of **10_000 sats** is included in all transactions
    

## 🛠 REPL Usage

Within the REPL, you can issue commands to interact with the Taproot Assets and Ark Protocol. Some example commands include:

- **Issue a  100k Token for the Boarding User :**

  ```bash
  >> mint
  Asset ID: 10289bf353cbca99715b138852dda43fa60d813384874557870080f1ebfde5e3
  2025/03/31 15:44:49 -------------------------------------
  2025/03/31 15:44:49 Minting Complete
  2025/03/31 15:44:49 ----------------------------------------------
  ```
- **Get A BTC Deposit Address for the Onboarding User :**
    ```bash
    >> deposit
    2025/03/31 15:52:37 
    Deposit Address For Onboarding User : bcrt1p7zaqe4qddflre826pk8zu64xt3pl690mgav7cqmrq95984v5f78skq72mv
    2025/03/31 15:52:37 -------------------------------------
    2025/03/31 15:52:37 Deposit Address Gotten
    2025/03/31 15:52:37 -----------------------------------------------
    ```

- **List Balances For both the Onboarding User and Exit User:**

  ```bash
  >> balance
  2025/03/31 15:49:55 ------Boarding User-----
  2025/03/31 15:49:55 Asset Balance = 100000
  2025/03/31 15:49:55 Btc Balance = 298760564
  2025/03/31 15:49:55 -------------------------------------
  2025/03/31 15:49:55 -----Exit User------
  2025/03/31 15:49:55 Asset Balance = 0
  2025/03/31 15:49:55 Btc Balance = 442500
  2025/03/31 15:49:55 -------------------------------------
  ```

- **Onboard 40 tokens and 100_000 sats to the Ark Server**

  ```bash
  >> board
  2025/03/31 15:59:30 Boarding Asset TxId 3b0d25dca57a62fca3184337c02d09e1b132bddeb2eb0794c519517369b3dd64
  2025/03/31 15:59:30 Boarding BTC TxId fa501e10e8c3d9448934d78b44f9ed2da6f4e7ffd5a9d7589c4cf2b271827f44
  2025/03/31 15:59:30 awaiting block to be mined
  2025/03/31 15:59:58 Boarding User Complete
  2025/03/31 15:59:58 ------------------------------------------------
  ```

- **Create and Broadcast a Round transaction:**

  ```bash
  >> round
  2025/03/31 16:12:15 awaiting confirmation
  2025/03/31 16:12:21 
  Round Transaction Hash 38ebaa12c552231903d4f37dad6c6573223ddf87045d5f7541f8c664daf1784a
  2025/03/31 16:12:21 Round Construction Complete
  2025/03/31 16:12:21 ------------------------------------------------
  ```

- **Create and Broadcast a Round transaction:**

  ```bash
  >> round
  2025/03/31 16:12:15 awaiting confirmation
  2025/03/31 16:12:21 
  Round Transaction Hash 38ebaa12c552231903d4f37dad6c6573223ddf87045d5f7541f8c664daf1784a
  2025/03/31 16:12:21 Round Construction Complete
  2025/03/31 16:12:21 ------------------------------------------------
  ```

- **View vtxos and intermediate transactions:**

  ```bash
  >> vtxos
  2025/03/31 16:19:07 ------Intermediate Transaction-----
  2025/03/31 16:19:07 Left Output:  Asset Amount = 20, Btc Amount = 40500
  2025/03/31 16:19:07 Right Output:  Asset Amount = 20, Btc Amount = 40500
  2025/03/31 16:19:07 
  Transaction Hash: 557c63c1916b3baed543d6ed3b423f31d90b8a515b77561ffb54d7995c8819ff
  2025/03/31 16:19:07 -------------------------------------
  2025/03/31 16:19:07 ------Left Leaf Transaction-----
  2025/03/31 16:19:07 Left Output:  Asset Amount = 20
  2025/03/31 16:19:07 Right Output: Btc Amount = 29500
  2025/03/31 16:19:07 
  Transaction Hash: b1c6baac2a58b563fa4dd91a68dfe323120fde246a8175f581ec29c31ca4c2b4
  2025/03/31 16:19:07 -------------------------------------
  2025/03/31 16:19:07 ------Right Leaf Transaction-----
  2025/03/31 16:19:07 Left Output:  Asset Amount = 20
  2025/03/31 16:19:07 Right Output: Btc Amount = 29500
  2025/03/31 16:19:07 
  Transaction Hash: 59e97f3ddc105eaa44214d5e2b128d975a1dee6ab07458602e70a80af976384a
  2025/03/31 16:19:07 -------------------------------------
    ```

 - **Publish Exit Transactions and Upload Asset Proofs to Exit User:**
  ```bash
  >> upload
  2025/03/31 16:29:11 awaiting confirmation
  2025/03/31 16:29:21 awaiting confirmation
  2025/03/31 16:29:51 awaiting confirmation
  2025/03/31 16:30:21 Exit Proof appended
  2025/03/31 16:30:21 Exit Proof Imported
  2025/03/31 16:30:21 Exit Transactions Broadcasted and Proofs Uploaded
  2025/03/31 16:30:21 ------------------------------------------------
  ```
 
 - **Exit The Repl**
  ```bash
  >> exit
  Goodbye!
  ```

  **Note**: Please ensure that a minimum total of 200,000 sats is deposited for the Onboarding User, divided into two installments of 100,000 sats each. This deposit is essential for both Minting and creating the Boarding Transaction.

---

## 📁 Project Structure

```
taponark/
├── cmd/
    ├── app.go            # Core logic for the interactive commands 
│   └── main.go           # The REPL entry point for interactive commands
├── ark.go                # Logic necessary for the creation of Ark Specfic Boarding and Round Spending Condition
│                           Also contains condition for signing Ark Specific Boarding and Boarding Scripts
├── boarding.go           # Contains the construction and broadcastiong  of BTC boarding transaction and Asset          │                           Boarding Transaction
├── round.go              # Contains Logic to construct round, round tree offchain transactions and broadcast the round │                           transaction
├── proof.go              # Contains Logic to update asset transfer proofs and to publish such transfer proofs to tapd
├── bcoin.go              # Bitcoind specific RPC interaction logic  
├── lnd.go                # Lnd specific GRPC interaction logic
├── tap.go                # Tapd specific GRPC interaction logic
├── config.go             # Includes Configuration Details for Server, Onboarding User and Boarding User   
└── README.md
```

---

## Considerations

To simplify the import of asset proofs needed for our proof-of-concept, we employed a modified Tapd daemon that exposes a development RPC called **ImportProof**.


## 🧠 Motivation

This proof-of-concept explores the feasibility of integrating Taproot Assets into the Ark Protocol. By leveraging a Dockerized network and an interactive REPL.

---

## 📜 License

MIT

---

