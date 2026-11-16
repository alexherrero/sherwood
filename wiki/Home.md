# Sherwood

Sherwood is a small, self-hosted investment tool. It runs on a home NAS, keeps a portfolio of at most 20 stocks, trades by fixed written rules inside hard limits, and keeps an AI-written feed of possible next picks. You reach it from your phone over a private Tailscale network.

## Designs

- **[Sherwood Design](designs/sherwood)** — is the design for the rewrite. It explains why sherwood is built the way it is: the rules that make every trade, the risk check every order passes through, the approval step for buys, and why the AI never touches the broker. It is a draft awaiting review.

The original proof-of-concept is archived in [`v1/`](https://github.com/alexherrero/sherwood/tree/main/v1).
