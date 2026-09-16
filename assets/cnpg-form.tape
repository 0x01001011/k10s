# CNPG parameter form — the clip for the PR that introduced it.
#
#   vhs assets/cnpg-form.tape        (or `just demo-cnpg`)
#
# Needs `k10s` on PATH (`just install`) and charmbracelet/vhs installed.
# Runs the offline demo backend, so no real cluster reaches the docs — see
# docs/build-in-public.md.
#
# What the clip has to show, in order, because each answers a question the PR
# makes a claim about:
#
#   1. the PG Clusters table, where orders-db reports "Cluster in healthy
#      state" and its WAL column reads False
#   2. fence opening a FORM, not a free-text box
#   3. typing filtering the instance list, with the primary marked
#   4. the kubectl preview changing as the instance is chosen
#   5. the typed gate asking for the INSTANCE, not the cluster

# Quoted: vhs' parser splits an unquoted path on the hyphen and reports three
# errors while still printing "Creating ...". assets/demo.tape gets away
# without quotes only because "demo.gif" has no hyphen in it.
Output "assets/cnpg-form.gif"

Set Shell "bash"
Set FontFamily "JetBrainsMono Nerd Font"
Set FontSize 15
Set Width 1500
Set Height 800
Set Padding 20
Set Theme { "background": "#1a1b26", "foreground": "#c0caf5", "cursor": "#c0caf5" }

Env K10S_CONFIG "/tmp/k10s-demo-config.yaml"

Type "k10s demo"
Enter
Sleep 3s

Escape
Sleep 1s

# Lens kinds live on the -prod context only: packs are discovery-gated, and a
# demo where every context ran every operator would teach the wrong thing.
Type ":ctx k10s-demo-prod"
Enter
Sleep 2s

# The table. orders-db is top because it is worst: phase says healthy, WAL
# says False. That pairing is the whole argument for the column.
Type ":pgc"
Enter
Sleep 1s
Escape
Sleep 3s

# [7] Fence instance. A form opens with the cluster's own instances.
Type "7"
Sleep 3s

# Typing narrows the list; the note says which one is the primary.
Type "orders"
Sleep 2s
Down
Sleep 2s

# Enter takes the highlighted instance. The kubectl line below rewrites itself
# to name it — this is the frame the PR's "preview tracks the form" claim rests
# on.
Enter
Sleep 3s

# Enter again moves to the typed gate, which asks for the INSTANCE. Typing the
# cluster's name would not arm it.
Enter
Sleep 2s
Type "orders-db-2"
Sleep 3s

# Cancelled rather than sent: the demo's write is a pretend one, and a clip
# that ends on a fence is a clip somebody will copy onto a real cluster.
Escape
Sleep 1s
Escape
Sleep 2s
