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

Type "aisle list"
Sleep 400ms
Enter
Sleep 3.5s

Type "aisle"
Sleep 300ms
Enter
Sleep 2.5s
Down
Sleep 400ms
Down
Sleep 700ms
Type "/"
Sleep 300ms
Type "deploy"
Sleep 1.8s
Enter
Sleep 900ms
Enter
Sleep 3.5s

# detach from tmux and look again: the conversation is now live
Ctrl+B
Type "d"
Sleep 1s
Type "clear && aisle list --project infra"
Enter
Sleep 5s

Hide
Type "tmux kill-server; rm -rf $D"
Enter
