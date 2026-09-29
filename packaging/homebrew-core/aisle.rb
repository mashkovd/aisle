class Aisle < Formula
  desc "Cross-agent session navigator for Claude Code, Codex, Gemini CLI and Antigravity"
  homepage "https://github.com/mashkovd/aisle"
  url "https://github.com/mashkovd/aisle/archive/refs/tags/v0.4.0.tar.gz"
  sha256 "7c3b5f8338903bccc16be614b77553e1bb9f8750326926b1b13a0d005c284b5c"
  license "MIT"
  head "https://github.com/mashkovd/aisle.git", branch: "main"

  depends_on "go" => :build
  depends_on "tmux"

  def install
    system "go", "build", *std_go_args(ldflags: "-s -w -X main.version=#{version}"), "./cmd/aisle"
    generate_completions_from_executable(bin/"aisle", "completion")
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/aisle version")
    ENV["HOME"] = testpath
    assert_match "ENGINE", shell_output("#{bin}/aisle list")
    (testpath/"AGENTS.md").write "# Rules\n"
    assert_match "reads it", shell_output("#{bin}/aisle rules check #{testpath}", 1)
  end
end
