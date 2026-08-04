# Manifest-list digest verified 2026-08-04.
FROM maven:3.9.9-eclipse-temurin-21@sha256:3a4ab3276a087bf276f79cae96b1af04f53731bec53fb2e651aca79e4b10211e AS smt-build

WORKDIR /src
COPY pseudonymizer/pom.xml pseudonymizer/pom.xml
COPY pseudonymizer/src pseudonymizer/src
RUN mvn -q -f pseudonymizer/pom.xml -DskipTests package

# The 3.1.2.Final image is published by the upstream Debezium project in
# quay.io; Docker Hub does not carry this tag. Manifest-list digest verified
# 2026-08-04.
FROM quay.io/debezium/connect:3.1.2.Final@sha256:1290a7abb0c1fe957f8d732e43efbea24201d9ba0fa5ba79582f76438c05d0ba

USER root
COPY --from=smt-build /src/pseudonymizer/target/seev-analytics-pseudonymizer-1.0.0.jar /kafka/connect/seev-analytics-pseudonymizer/
USER kafka
