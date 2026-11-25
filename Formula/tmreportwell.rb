# Homebrew formula for tmreportwell. This repository is its own tap.
#
# The release build rewrites the version line, the two sha256 lines by their
# marker comments and the bottle block between the BOTTLE markers. Keep the
# markers, and keep each url line directly above its sha256 line.
class Tmreportwell < Formula
  desc "Mail a daily report about Time Machine backups"
  homepage "https://github.com/moetiker/TmReportWell"
  version "0.0.0"
  license "MIT"

  # BOTTLE-START
  # BOTTLE-END

  depends_on :macos

  on_arm do
    url "https://github.com/moetiker/TmReportWell/releases/download/v#{version}/tmreportwell-#{version}-aarch64-apple-darwin.tar.gz"
    sha256 "0000000000000000000000000000000000000000000000000000000000000000" # mac-arm
  end
  on_intel do
    url "https://github.com/moetiker/TmReportWell/releases/download/v#{version}/tmreportwell-#{version}-x86_64-apple-darwin.tar.gz"
    sha256 "0000000000000000000000000000000000000000000000000000000000000000" # mac-x86
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
