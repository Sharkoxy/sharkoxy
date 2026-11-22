<p align="center">
  <img src="docs/banner.gif" alt="Sharkoxy: the shark mascot jumps over the wall" width="100%">
</p>

<p align="center">
  <a href="../../releases/latest"><img src="https://img.shields.io/badge/DOWNLOAD-WINDOWS-00e436?style=for-the-badge&logo=windows&logoColor=white" alt="Download for Windows"></a>
</p>

<p align="center">
  <img src="https://img.shields.io/github/v/release/Sharkoxy/sharkoxy?style=flat-square&color=29adff" alt="Latest release">
  <img src="https://img.shields.io/github/license/Sharkoxy/sharkoxy?style=flat-square&color=ffec27" alt="GPL-3.0">
  <img src="https://img.shields.io/github/downloads/Sharkoxy/sharkoxy/total?style=flat-square&color=00e436" alt="Downloads">
  <img src="https://img.shields.io/badge/platform-Windows%2010%2F11-83769c?style=flat-square" alt="Windows 10/11">
</p>

<h3 align="center">Paste your subscription. Press the button. Stay on the fastest server.</h3>

<p align="center">
Sharkoxy is a free, open-source proxy client for Windows.<br>
It tests your servers, connects to the fastest one, and switches by itself when it slows down or dies.
</p>

---

## Why you'll like it

<table>
  <tr>
    <td width="33%" align="center"><img src="docs/icon-fast.png" width="96"><br><b>Always the fastest</b><br>Tests every server several times and moves you to the best one.</td>
    <td width="33%" align="center"><img src="docs/icon-stable.png" width="96"><br><b>Built to stay connected</b><br>Spots dead servers, recovers from crashes, re-tests after sleep.</td>
    <td width="33%" align="center"><img src="docs/icon-map.png" width="96"><br><b>World map</b><br>See where your servers are and how quick each one is. Zoom in.</td>
  </tr>
  <tr>
    <td align="center"><img src="docs/icon-profiles.png" width="96"><br><b>Many subscriptions</b><br>Add as many as you like. Paste from the clipboard or click a `sharkoxy://` link in your browser.</td>
    <td align="center"><img src="docs/icon-update.png" width="96"><br><b>Safe auto-updates</b><br>Every update is signed and checked before it is installed.</td>
    <td align="center"><img src="docs/icon-look.png" width="96"><br><b>Two looks + live graph</b><br>Retro 8-bit arcade or a clean, calm style. Your choice.</td>
  </tr>
</table>

## Screenshots

<table>
  <tr>
    <td align="center"><img src="docs/screenshot-8bit.png"><br><sub>8-bit Arcade</sub></td>
    <td align="center"><img src="docs/screenshot-clean.png"><br><sub>Clean &amp; Soft</sub></td>
  </tr>
</table>

## Get started

1. Download **Sharkoxy-Setup-Full.exe** from the [Releases](../../releases/latest) page and run it. The sing-box engine is included, so nothing else needs downloading.
2. Windows may say "Windows protected your PC" because the app is new. Click **More info**, then **Run anyway**.
3. Paste your subscription link, press the big button, and watch the shark jump the wall.

## Build from source

You need Go 1.24. The workflow in `.github/workflows` shows the exact steps.

## Privacy

Sharkoxy has no accounts, ads or analytics, and sends nothing about you to the project. Your subscription links and settings stay on your computer. The app only contacts: your own subscription provider (to refresh servers), GitHub (to check for signed updates, through your proxy when connected), and the sites you test in the Connectivity box.

## Code signing policy

Release builds are made automatically from this repository by GitHub Actions, and every release lists SHA-256 checksums. Windows may show an "unknown publisher" notice until the project has a code-signing certificate. Only download Sharkoxy from the Releases page of this repository.

## About

Made by Shahriar Rahman, an undergraduate student, as a side project. The story is in **About Sharkoxy.docx**. Contact: bookcollectionforcloud@gmail.com

## License

GPL-3.0. Sharkoxy uses [sing-box](https://github.com/SagerNet/sing-box), see `NOTICE`.
