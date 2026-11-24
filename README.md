<p align="center">
  <img src="docs/banner.gif" alt="Sharkoxy: the shark mascot jumps over the wall" width="100%">
</p>

<p align="center">
  <a href="../../releases/latest"><img src="https://img.shields.io/badge/DOWNLOAD-FOR%20WINDOWS-00e436?style=for-the-badge&logo=windows&logoColor=white" alt="Download for Windows"></a>
</p>

<p align="center">
  <img src="https://img.shields.io/github/v/release/Sharkoxy/sharkoxy?style=flat-square&color=29adff" alt="Latest release">
  <img src="https://img.shields.io/github/downloads/Sharkoxy/sharkoxy/total?style=flat-square&color=00e436" alt="Downloads">
  <img src="https://img.shields.io/github/stars/Sharkoxy/sharkoxy?style=flat-square&color=ffec27" alt="Stars">
  <img src="https://img.shields.io/github/license/Sharkoxy/sharkoxy?style=flat-square&color=ffa300" alt="GPL-3.0">
  <img src="https://img.shields.io/badge/Windows-10%20%7C%2011-83769c?style=flat-square&logo=windows&logoColor=white" alt="Windows 10 and 11">
  <img src="https://img.shields.io/badge/engine-sing--box-ff77a8?style=flat-square" alt="Powered by sing-box">
</p>

<h3 align="center">Paste your subscription. Press one button. Stay on the fastest server.</h3>

<p align="center">
Sharkoxy is a free, open-source proxy client for Windows.<br>
It tests your servers, connects you to the fastest one, and switches by itself when a server slows down or dies.<br>
No accounts. No ads. No tracking. The engine is built in.
</p>

<p align="center">
  <a href="#-features">Features</a> ·
  <a href="#-looks">Looks</a> ·
  <a href="#-get-started">Get started</a> ·
  <a href="#-faq">FAQ</a> ·
  <a href="#-roadmap">Roadmap</a> ·
  <a href="#-privacy">Privacy</a>
</p>

---

## ✨ Features

<table>
  <tr>
    <td width="33%" align="center"><img src="docs/icon-fast.png" width="84"><br><b>Always the fastest</b><br>Times every server several times and moves you to the best one, without cutting your downloads or calls for a tiny gain.</td>
    <td width="33%" align="center"><img src="docs/icon-stable.png" width="84"><br><b>Built to stay connected</b><br>Spots dead servers, restarts the engine if it gets stuck, recovers from network changes and re-tests after sleep.</td>
    <td width="33%" align="center"><img src="docs/icon-map.png" width="84"><br><b>Zoomable world map</b><br>See where your servers are and how quick each one is.</td>
  </tr>
  <tr>
    <td align="center"><img src="docs/icon-speed.png" width="84"><br><b>Live speed graph</b><br>Download and upload speed, average ping and peak, right on the main screen.</td>
    <td align="center"><img src="docs/icon-profiles.png" width="84"><br><b>Many subscriptions</b><br>Add as many as you like. Their servers are combined and the fastest overall wins. Each refreshes by itself.</td>
    <td align="center"><img src="docs/icon-link.png" width="84"><br><b>Easy import</b><br>Paste from the clipboard, or click a <code>sharkoxy://</code> link on your provider's website. You confirm before anything is added.</td>
  </tr>
  <tr>
    <td align="center"><img src="docs/icon-look.png" width="84"><br><b>Three looks</b><br>Clean &amp; Soft, Midnight, or retro 8-bit Arcade. Switch any time.</td>
    <td align="center"><img src="docs/icon-update.png" width="84"><br><b>Safe auto-updates</b><br>Every update is signed and verified before it is installed. A "Check for updates" button is in Settings.</td>
    <td align="center"><img src="docs/icon-cat.png" width="84"><br><b>Friendly</b><br>A shark mascot cheers you on while you connect, and a cat with a cup of tea tells you what's new after an update.</td>
  </tr>
</table>

**Also included:** smart routing (sites in China go direct, everything else through the fastest server; or send everything through the proxy), system proxy on/off, share the connection with other devices on your network, custom rules, launch at login, silent installer for scripts and package managers (`/S`), and a tidy uninstaller that restores your Windows proxy settings.

## 🎨 Looks

<p align="center"><img src="docs/themes.png" alt="Clean and Soft, Midnight and 8-bit Arcade themes" width="100%"></p>

<table>
  <tr>
    <td width="33%" align="center"><b>Clean &amp; Soft</b><br><sub>The default. Calm, friendly, follows Windows light and dark mode.</sub></td>
    <td width="33%" align="center"><b>Midnight</b><br><sub>Minimal, monospace, pure black. Nothing but the essentials.</sub></td>
    <td width="33%" align="center"><b>8-bit Arcade</b><br><sub>Pixel fonts, a pixel map and a mascot that jumps the wall.</sub></td>
  </tr>
</table>

### After every update

<p align="center"><img src="docs/update-popup.gif" alt="Animated pixel cat next to a steaming cup of tea, with the what's new list" width="420"></p>

## 🚀 Get started

| Download | What it is |
|---|---|
| **[Sharkoxy-Setup-Full.exe](../../releases/latest)** | **Recommended.** The installer with the sing-box engine included. Nothing else to download. |
| [Sharkoxy-Setup.exe](../../releases/latest) | The smaller installer, without the bundled engine. |
| [Sharkoxy.exe](../../releases/latest) | The app on its own, for people who want to place it themselves. |

1. Download **Sharkoxy-Setup-Full.exe** from the [Releases](../../releases/latest) page and run it.
2. Windows may say "Windows protected your PC" because the project is new (see the [FAQ](#-faq)). Click **More info**, then **Run anyway**.
3. Paste your subscription link, press the big button, and watch the shark jump the wall.

Sharkoxy understands share links, Clash profiles and sing-box profiles. It needs Windows 10 or 11 with the Microsoft Edge WebView2 runtime, which comes with current Windows.

**Silent install (scripts):** `Sharkoxy-Setup-Full.exe /S`

## ❓ FAQ

<details>
<summary><b>Windows says "unknown publisher". Is Sharkoxy safe?</b></summary>
<br>
Windows shows that screen for any new app that is not yet code-signed, and a signing certificate costs money. Sharkoxy is fully open source (GPL-3.0) and the releases are built automatically from this repository by GitHub Actions, so you can read exactly what you are running. Only download it from this page.
</details>

<details>
<summary><b>Where do I get a subscription?</b></summary>
<br>
Sharkoxy does not sell or provide servers. You bring your own subscription link from a provider you already use.
</details>

<details>
<summary><b>The connection dropped. What does Sharkoxy do?</b></summary>
<br>
It checks the connection continuously. If a server dies it moves you to the next fastest one. If the network changes or the engine gets stuck it restarts the engine by itself, and it keeps trying until you disconnect.
</details>

<details>
<summary><b>Where is my data?</b></summary>
<br>
On your computer, in <code>%APPDATA%\Sharkoxy</code>. Uninstalling asks before deleting it, and always restores your Windows proxy settings.
</details>

<details>
<summary><b>Are updates safe?</b></summary>
<br>
Yes. Each update is signed with a private key that is not in this repository. The app checks the signature, file size and checksum before installing, and asks before restarting.
</details>

<details>
<summary><b>Android and iOS?</b></summary>
<br>
Planned. Windows comes first and has to be solid, then Android, then iOS.
</details>

<details>
<summary><b>Something is wrong. How do I report it?</b></summary>
<br>
Open an <a href="../../issues">issue</a> and attach <code>app.log</code> from <code>%APPDATA%\Sharkoxy</code>.
</details>

## 🗺️ Roadmap

- [x] Windows app with the engine built in
- [x] Fastest-server selection, failover and self-healing
- [x] Multiple subscriptions, clipboard and browser-link import
- [x] Signed auto-updates with a "what's new" note
- [x] Three looks, live speed graph, world map
- [ ] `winget install Sharkoxy`
- [ ] Code-signed installer
- [ ] Android app
- [ ] iOS app

## 🔒 Privacy

Sharkoxy has no accounts, ads or analytics, and sends nothing about you to the project. Your subscription links and settings stay on your computer. The app only contacts: your own subscription provider (to refresh servers), GitHub (to check for signed updates, through your proxy when connected), and the sites you test in the Connectivity box.

## 🛡️ Code signing policy

Release builds are made automatically from this repository by GitHub Actions. Windows may show an "unknown publisher" notice until the project has a code-signing certificate. Only download Sharkoxy from the Releases page of this repository.

## 👋 About

Sharkoxy is made by **Shahriar Rahman**, who wanted a proxy app that simply stays connected, picks the best server by itself and is pleasant to use. The story is in **[About Sharkoxy.docx](About%20Sharkoxy.docx)**.

Questions and ideas are welcome in [Issues](../../issues), or by email: bookcollectionforcloud@gmail.com

If Sharkoxy helps you, a ⭐ on this page is the best way to say thanks, and it helps other people find it.

## 📜 License

GPL-3.0. Sharkoxy uses [sing-box](https://github.com/SagerNet/sing-box), see `NOTICE`.
