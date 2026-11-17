# Reference environment for building docs/install.pdf (`make docs-docker`):
# Ubuntu 24.04 with its own pandoc (3.1) and TeX Live (2023).
#
# The same package list works on a plain Ubuntu 24.04 machine:
#   sudo apt install make pandoc texlive-latex-recommended texlive-latex-extra \
#        texlive-fonts-recommended lmodern
FROM ubuntu:24.04
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      make pandoc texlive-latex-recommended texlive-latex-extra \
      texlive-fonts-recommended lmodern \
 && apt-get clean
WORKDIR /src
