# Records the README demo on synthetic data:  vhs demo/aisle.tape
Output demo/aisle.gif

Require tmux
Set Shell "bash"
Set FontSize 17
Set Width 1400
Set Height 680
Set Padding 18
Set Theme "Catppuccin Mocha"
Set TypingSpeed 55ms
Set CursorBlink false

Hide
Type "export D=$(mktemp -d) && python3 demo/seed.py $D && go build -o $D/aisle ./cmd/aisle"
Enter
Type "cp demo/tmux.conf $D/.tmux.conf"
Enter
Type "export HOME=$D PATH=$D:$PWD/demo/bin:$PATH TMUX_TMPDIR=$D PS1='\[\e[35m\]❯\[\e[0m\] ' && cd ~/work/api && bash ~/live.sh && clear"
Enter
Sleep 3s
Show

# a Claude session waits on a permission prompt, a Codex one is working
Type "aisle"
Sleep 300ms
Enter
Sleep 4s

# full-text search: "cookie" is only in the agents' replies
Type "f"
Sleep 400ms
Type "cookie"
Sleep 700ms
Enter
Sleep 2.5s
Escape
Sleep 1.2s

# jump into the session that needs you and answer it
Enter
Sleep 3s
Type "1"
Sleep 3s
Ctrl+B
Type "d"
Sleep 1s

# a new session in its own git worktree
Type "clear && aisle new claude --worktree=fix-cookie"
Sleep 400ms
Enter
Sleep 3.5s
Ctrl+B
Type "d"
Sleep 1s

Type "clear && aisle list -n 4 && ls .worktrees && git branch"
Enter
Sleep 6s

Hide
Type "tmux kill-server; rm -rf $D"
Enter
