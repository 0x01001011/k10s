# k10s demo — keyboard-only, reproducible clip for docs and release notes.
#
#   vhs assets/demo.tape        (or `just demo`)
#
# Needs `k10s` on PATH (`just install`) and charmbracelet/vhs installed.
# `k10s demo` runs the offline demo backend, which is what every published
# frame should use — see docs/build-in-public.md. Plain `k10s` would show
# whatever cluster the recording machine can reach, or none at all.
#
# The mouse is k10s' differentiator and vhs cannot record a cursor: pair this
# with a real screen capture for the hero clip.

Output assets/demo.gif

Set Shell "bash"
Set FontFamily "JetBrainsMono Nerd Font"
Set FontSize 15
Set Width 1500
Set Height 760
Set Padding 20
Set Theme { "background": "#1a1b26", "foreground": "#c0caf5", "cursor": "#c0caf5" }

# The same throwaway config the hero screenshot uses: onboarding already seen
# and the update check off, so the clip shows k10s rather than a release notice
# that happens to be pending on the day it was recorded. `just demo` writes it.
Env K10S_CONFIG "/tmp/k10s-demo-config.yaml"

Type "k10s demo"
Enter
Sleep 3s

# dismiss the first-run onboarding
Escape
Sleep 1s

# down to the CrashLoopBackOff pod, then follow its logs
Down
Down
Down
Sleep 1s
Type "l"
Sleep 3s
Escape
Sleep 1s

# search everything: kinds and objects in one box
Ctrl+P
Sleep 1s
Type "svc"
Sleep 2s
Enter
Sleep 2s

# theme picker — applies each theme live as you move
Type "/theme"
Enter
Sleep 1s
Down
Sleep 1s
Down
Sleep 1s
Down
Sleep 1s
Escape
Sleep 1s

# A lens kind, then X: the Kargo promotion DAG drawn as a tree, three hops
# deep, each stage carrying its own graded cells.
#
# AI (`ctrl+a`) is deliberately not in this clip. It needs a provider key, and
# a recording machine without one falls through to the shell, which puts a
# shell error in the hero GIF instead of an answer.
#
# The demo's lens kinds live on the -prod context only, because lens packs are
# discovery-gated and a demo where every context ran every operator would
# teach the wrong thing about the real backend.
Type ":ctx k10s-demo-prod"
Enter
Sleep 2s
Type ":stages"
Enter
Sleep 2s
# Escape closes the prompt, Right hands the table the focus, Down walks the
# stages: worst-first, so the Unhealthy/Failed one is already on top.
#
# `X` (the tree) is NOT in this clip. Under vhs the keypress never reaches the
# handler — verified across three recordings, with the same key working in
# cmd/shot — so recording it would ship five dead seconds. Re-add it here once
# that is understood; docs/lenses.md carries a real frame of the tree already.
Escape
Sleep 1s
Right
Sleep 1s
Down
Sleep 2s
Down
Sleep 3s
