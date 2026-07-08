class Icode < Formula
  desc "Multi-Model AI Coding Agent — terminal-native, multi-provider"
  homepage "https://github.com/ponygates/icode"
  url "https://github.com/ponygates/icode/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "REPLACE_WITH_ACTUAL_SHA256"
  license "Apache-2.0"
  head "https://github.com/ponygates/icode.git", branch: "master"

  depends_on "go" => :build

  def install
    system "go", "build", "-ldflags", "-s -w -X main.Version=#{version}", "-o", bin/"icode", "."
  end

  test do
    system "#{bin}/icode", "--version"
  end
end
