# Homebrew formula for tmreportwell. This repository is its own tap.
#
# The release build rewrites the version line, the two sha256 lines by their
# marker comments and the bottle block between the BOTTLE markers. Keep the
# markers, and keep each url line directly above its sha256 line.
class Tmreportwell < Formula
  desc "Mail a daily report about Time Machine backups"
  homepage "https://github.com/moetiker/TmReportWell"
  version "0.0.1"
  license "MIT"

  # BOTTLE-START
  bottle do
    root_url "https://github.com/moetiker/TmReportWell/releases/download/v0.0.1"
    sha256 cellar: :any_skip_relocation, arm64_ventura: "13397486d7f8c63813fde98f4cbb2dec4273588aef6a6e3d20e0e1f1c5a4a2d0"
  end
  # BOTTLE-END

  depends_on :macos

  on_arm do
    url "https://github.com/moetiker/TmReportWell/releases/download/v#{version}/tmreportwell-#{version}-aarch64-apple-darwin.tar.gz"
    sha256 "86da2b2d3721a04f960921b4f510c1052e3adfdf563c2974abd226dfe0296849" # mac-arm
  end
  on_intel do
    url "https://github.com/moetiker/TmReportWell/releases/download/v#{version}/tmreportwell-#{version}-x86_64-apple-darwin.tar.gz"
    sha256 "026b84cfc6050df352927512454efe73d90f8fd9b39919b6b19706f1c55bcdd1" # mac-x86
  end

  def install
    bin.install "tmreportwell"
    Dir["man/*.[1-9]"].each { |page| (man/"man#{page[-1]}").install page }
    pkgshare.install "config.example.json"
  end

  def caveats
    <<~EOS
      Set up the configuration and the keychain item as the README describes, then run:
        tmreportwell send
        tmreportwell install
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/tmreportwell --version")
  end
end
