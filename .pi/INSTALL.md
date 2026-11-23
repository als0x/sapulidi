# sapulidi on Pi

Status: untested. The steps below follow Pi's public documentation.

sapulidi is one skill (`skills/sapulidi`) plus a launcher (`bin/sapulidi`) that downloads and verifies the
binary on first use. Pi has no session hooks here.

1. Clone the repository somewhere stable: `git clone https://github.com/als0x/sapulidi`.
2. Make the skill visible to Pi: link `skills/sapulidi` into the directory Pi loads skills from. Keep the
   repository layout: the skill finds the launcher at `<skill dir>/../../bin/sapulidi`, so a symlink to the
   directory inside the clone works and a bare copy does not.
3. Ask: "use the sapulidi skill". The skill reads `references/pi.md`.
