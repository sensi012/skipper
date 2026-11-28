class Skipper < Formula
  desc "Intent-first developer platform CLI for Docker and Kubernetes"
  homepage "https://github.com/sensi012/skipper"
  version "0.1.1"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/sensi012/skipper/releases/download/v0.1.1/skipper-darwin-arm64"
      sha256 "b803a27dccd27e95515dd7e4c0254651ae9d2ec225bacfe7f9149dad2931e9d4"
    else
      url "https://github.com/sensi012/skipper/releases/download/v0.1.1/skipper-darwin-amd64"
      sha256 "190a9a61ded24e92c9708f237e9817fa081472fa6da41367f942c1384a410e5a"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/sensi012/skipper/releases/download/v0.1.1/skipper-linux-arm64"
      sha256 "810009fedbc436900167a9da2670db010809635eeb2f9174df0c064c11d89700"
    elsif Hardware::CPU.intel?
      url "https://github.com/sensi012/skipper/releases/download/v0.1.1/skipper-linux-amd64"
      sha256 "c2c6c63d961d69e8f48bfba3bf849a84326fe148d0b1308a6d24b026da0211b1"
    end
  end

  def install
    binary = Dir["skipper-*"].first
    bin.install binary => "skipper"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/skipper --version")
  end
end
