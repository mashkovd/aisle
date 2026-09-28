# Records the README demo on synthetic data:  vhs demo/aisle.tape
Output demo/aisle.gif

Require tmux
Set Shell "bash"
Set FontSize 17
Set Width 1400
Set Height 640
Set Padding 18
Set Theme "Catppuccin Mocha"
Set TypingSpeed 55ms
Set CursorBlink false

Hide
Type "export D=$(mktemp -d) && python3 demo/seed.py $D && go build -o $D/aisle ./cmd/aisle"
Enter
Type "cp demo/tmux.conf $D/.tmux.conf"
Enter
Type "export HOME=$D PATH=$D:$PWD/demo/bin:$PATH TMUX_TMPDIR=$D PS1='\[\e[35m\]❯\[\e[0m\] ' && cd ~/work/api && clear"
Enter
Sleep 3s
Show

Type "aisle list -n 6"
Sleep 400ms
Enter
Sleep 3s

# full-text search: "cookie" is only in the agents' replies, not in any title
Type "aisle"
Sleep 300ms
Enter
Sleep 2s
Type "f"
Sleep 500ms
Type "cookie"
Sleep 900ms
Enter
Sleep 2.5s
Down
Sleep 800ms
Enter
Sleep 3.5s

# detach from tmux: the conversation is now live
Ctrl+B
Type "d"
Sleep 1s
Type "clear && aisle list --project api"
Enter
Sleep 5s

Hide
Type "tmux kill-server; rm -rf $D"
Enter
