FROM maven:3.9.9-eclipse-temurin-21 AS smt-build

WORKDIR /src
COPY pseudonymizer/pom.xml pseudonymizer/pom.xml
COPY pseudonymizer/src pseudonymizer/src
RUN mvn -q -f pseudonymizer/pom.xml -DskipTests package

FROM debezium/connect:3.1.2.Final

USER root
COPY --from=smt-build /src/pseudonymizer/target/seev-analytics-pseudonymizer-1.0.0.jar /kafka/connect/seev-analytics-pseudonymizer/
USER kafka
