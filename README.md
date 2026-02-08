# Sherwood 📈

A modular, proof-of-concept automated trading engine and management dashboard. This project provides a foundation for executing algorithmic trades, performing regression testing against historical data, and managing bot configurations via a React-based web interface.

## 🚀 Overview

`Sherwood` is designed for developers who want to move beyond basic scripts. It allows you to download historical data for stocks and crypto, validate your models through rigorous backtesting, and deploy them in either a **"Dry Run" (Paper Trading)** or **Live** environment.

## In Development

Sherwood is experimental and under active development. It is not expected to work nor should you consider it reliable for any purpose. Code here is intended to demonstrate the potential of AI-assisted software development and should not be used for any real-world trading.

<!--
### Key Features
* **Paper Trading (Dry Run):** Test your strategies in real-time using live market data without any financial risk.
* **Regression Testing:** Run your trading models against historical price action to analyze performance and refine logic.
* **Historical Reporting:** Integrated tools to download and visualize historical data for stocks and cryptocurrency from supported providers.
* **Web UI (TypeScript/React):** A clean dashboard for real-time tracking, reporting, and updating bot parameters on the fly.
* **Modular Provider System:** Native support for **Robinhood**, with a flexible plugin architecture to integrate other exchanges (Binance, Alpaca, etc.).
* **Dockerized Setup:** Simple deployment using Docker containers, ensuring your environment is consistent across local and cloud setups.

---

## 🛠 Tech Stack

* **Core Engine:** Node.js & TypeScript
* **Dashboard:** React & Tailwind CSS
* **Deployment:** Docker & Docker Compose
* **API Connectivity:** Robinhood (extensible via custom plugins)

---

## 📦 Getting Started

### 1. Prerequisites
* [Docker](https://www.docker.com/get-started) and Docker Compose installed.
* API credentials for your exchange provider.

### 2. Configuration
Access tokens and sensitive credentials are provided through environment variables. Create a `.env` file in the project root:

```env
# General Configuration
PORT=3000
TRADING_MODE=dry_run # Options: 'dry_run' or 'live'

# Robinhood Credentials
RH_USERNAME=your_email@example.com
RH_PASSWORD=your_password
RH_MFA_CODE=your_device_mfa_secret

# Historical Data API Key (if applicable)
HISTORICAL_DATA_TOKEN=your_token_here
