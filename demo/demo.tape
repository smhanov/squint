# Demo recording for the README (spec §14.7, §15): one vhs tape that
# walks the core workflow — select a commit, pick a file, read the hunk.
#
# Regenerate the GIF with:
#
#   vhs demo/demo.tape
#
# docs/demo.gif is regenerated on release; the recording needs the vhs
# binary (https://github.com/charmbracelet/vhs) plus ffmpeg.
Output demo/demo.gif
Set FontSize 20
Set Width 1100
Set Height 640
Set Theme "Dracula"

Type "cd squint-demo-repo"
Enter
Wait 300ms
Type "./squint --side-by-side"
Enter
Wait 2s
Key Down
Wait 1s
Key Tab
Wait 600ms
Key Down
Wait 600ms
Key Enter
Wait 1.5s
Key Tab
Wait 600ms
Key RightBracket
Wait 800ms
Key s
Wait 1.2s
Key i
Wait 1.2s
Key d
Wait 2s
Key q
Wait 800ms
