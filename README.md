# Sharkoxy

A fast, stable, open-source proxy client for Windows. Paste your subscription link, press the big button, and Sharkoxy connects you to the fastest server and keeps you there.

![Sharkoxy](docs/screenshot.png)

## Download

Get **Sharkoxy-Setup-Full.exe** from the [Releases](../../releases) page and run it. The sing-box engine is included, so nothing else needs downloading.

Windows may say "Windows protected your PC" because the app is new. Click **More info**, then **Run anyway**.

## Features

- Always connects to the fastest server and moves you if it slows down or dies
- Several subscriptions at once (Clash, sing-box and share-link formats)
- World map of your servers
- 8-bit Arcade and Clean & Soft looks
- Signed automatic updates

More about the project and its author: see **About Sharkoxy.docx**.

## Build from source

Needs Go 1.24. The release workflow in `.github/workflows` shows the exact steps.

## Privacy

Sharkoxy has no accounts, ads or analytics, and sends nothing about you to the project. Your subscription links and settings stay on your computer. The app only contacts: your own subscription provider (to refresh servers), GitHub (to check for signed updates, through your proxy when connected), and the sites you test in the Connectivity box.

## Code signing policy

Release builds are made automatically from this repository by GitHub Actions. Only the project owner, Shahriar Rahman, may approve a release for signing, and he is the sole committer, reviewer and approver. Signed files are the installers and `Sharkoxy.exe` published on the Releases page. Never download Sharkoxy from anywhere else.

## License

GPL-3.0. Sharkoxy uses [sing-box](https://github.com/SagerNet/sing-box), see `NOTICE`.
