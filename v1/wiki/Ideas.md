# Ideas

A running list of ideas for Sherwood's future direction. These are brainstorms and explorations — not yet committed to the roadmap. When an idea is ready to move forward, it should be refined into a feature spec and added to the [Roadmap](Roadmap).

---

## 1. Popular Trading Platform Feature Parity

**Added:** 2026-02-12

Research popular trading platforms (e.g., TradingView, Thinkorswim, Binance) and identify features that would improve Sherwood's usefulness to the average self-hosted trader. Implement the most impactful ones.

**Areas to explore:**

- Charting and technical indicators
- Social/copy trading
- Alerts and notifications
- Advanced order types (trailing stop, OCO, etc.)
- Watchlists and screeners

---

## 2. Cross-Platform Release Binaries & Docker

**Added:** 2026-02-12

Improve the release process to produce pre-built binaries and packages for **Windows**, **Linux**, and **macOS**, along with an official **Docker** image.

**Areas to explore:**

- GoReleaser or similar tooling for multi-platform builds
- GitHub Releases automation
- Docker Hub / GitHub Container Registry publishing
- Installer packages (`.msi`, `.deb`, `.rpm`, Homebrew tap)

---

## 3. Expanded Exchange Support

**Added:** 2026-02-12

Expand exchange integration beyond the current provider(s) to include other major players in the crypto and equities space.

**Potential exchanges:**

- Coinbase / Coinbase Pro
- Kraken
- Interactive Brokers
- Alpaca
- Bybit

**Considerations:**

- Unified broker interface abstraction
- Exchange-specific order types and limitations
- API rate limiting differences
