FROM eclipse-temurin:21-jdk
ENV KOTLIN_VERSION=2.4.10
RUN apt-get update && apt-get install -y --no-install-recommends curl unzip \
  && curl -L "https://github.com/JetBrains/kotlin/releases/download/v${KOTLIN_VERSION}/kotlin-compiler-${KOTLIN_VERSION}.zip" -o /tmp/kotlin.zip \
  && unzip /tmp/kotlin.zip -d /opt \
  && rm /tmp/kotlin.zip \
  && ln -s /opt/kotlinc/bin/kotlinc /usr/local/bin/kotlinc \
  && apt-get remove -y curl unzip \
  && apt-get autoremove -y \
  && rm -rf /var/lib/apt/lists/*
RUN mkdir -p /runner
COPY run.sh /runner/run.sh
RUN chmod +x /runner/run.sh
# Build-time smoke: verifies kotlinc + the run.sh JIT flags work and keeps
# the compiler jars hot in the image layers.
RUN echo 'fun main() { println("warm") }' > /tmp/Warm.kt \
  && export HOME=/tmp \
  && kotlinc -J-XX:TieredStopAtLevel=1 /tmp/Warm.kt -include-runtime -d /tmp/warm.jar \
  && java -jar /tmp/warm.jar \
  && rm -f /tmp/Warm.kt /tmp/warm.jar
