# Copy to Formula/huddle-gui.rb in a tap repo named homebrew-huddle, then: brew install <owner>/huddle/huddle-gui
class HuddleGui < Formula
  desc "Native macOS window for Claude Code's AskUserQuestion prompts"
  homepage "https://github.com/saimageshvar/huddle"
  license "MIT"
  head "https://github.com/saimageshvar/huddle.git", branch: "master"
  # For tagged releases add: url "https://github.com/saimageshvar/huddle/archive/refs/tags/vX.Y.Z.tar.gz" and its sha256

  depends_on :macos

  def install
    system "sh", "gui/build.sh"
    prefix.install "gui/Huddle.app"
    bin.install_symlink prefix/"Huddle.app/Contents/MacOS/huddle-hook" => "huddle-gui"
  end

  def caveats
    <<~EOS
      Register the Claude Code hook (safe to re-run after upgrades):
        huddle-gui setup
      Open the app by hand from:
        #{opt_prefix}/Huddle.app
    EOS
  end

  test do
    assert_match "usage: huddle-gui", shell_output("#{bin}/huddle-gui help")
  end
end
