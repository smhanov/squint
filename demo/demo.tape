# Demo recording for the README (spec §14.7, §15): one vhs tape that
# walks the core workflow — select a commit, pick a file, read the hunk.
#
# Regenerate the GIF with:
#
#   vhs demo/demo.tape
#
# The recording needs the vhs binary plus ffmpeg, and a scratch repo
# named squint-demo-repo in the checkout root with a ./squint binary.
Output demo/demo.gif
Set FontSize 16
Set Width 1400
Set Height 800
Set Theme "Dracula"

Type "cd squint-demo-repo"
Enter
Sleep 300ms
Type "./squint --side-by-side"
Enter
Sleep 2s
Down
Sleep 1s
Tab
Sleep 600ms
Down
Sleep 600ms
Enter
Sleep 1.5s
Tab
Sleep 600ms
Type "]"
Sleep 800ms
Type "s"
Sleep 1.2s
Type "i"
Sleep 1.2s
Type "d"
Sleep 2s
Type "q"
Sleep 800ms
