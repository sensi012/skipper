class Skipper < Formula
  desc "Intent-first developer platform CLI for Docker and Kubernetes"
  homepage "https://github.com/sensi012/skipper"
  version "0.1.0"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/sensi012/skipper/releases/download/v0.1.0/skipper-darwin-arm64"
      sha256 "REPLACE_WITH_DARWIN_ARM64_SHA256"
    else
      url "https://github.com/sensi012/skipper/releases/download/v0.1.0/skipper-darwin-amd64"
      sha256 "REPLACE_WITH_DARWIN_AMD64_SHA256"
    end
  end

  on_linux do
    if Hardware::CPU.intel?
      url "https://github.com/sensi012/skipper/releases/download/v0.1.0/skipper-linux-amd64"
      sha256 "REPLACE_WITH_LINUX_AMD64_SHA256"
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
