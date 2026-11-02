@lab @stormbreaker-group/delivery
Feature: Depot scans in Avro
  A stream of Avro events through a real topic and the Schema Registry: stormbreaker-kafka
  registers the schema and writes the Confluent wire format, and axx's kafka pack reads them back
  through the registry.

  Scenario: Avro depot scans, through the Schema Registry
    Given the events kafka service with the following properties:
      | brokers | ${sys:kafka.brokers} |
    And a depot-scans kafka topic client with the following properties:
      | producer.value.serializer    | io.confluent.kafka.serializers.KafkaAvroSerializer   |
      | producer.schema.registry.url | ${sys:schema-registry.url}                           |
      | consumer.value.deserializer  | io.confluent.kafka.serializers.KafkaAvroDeserializer |
      | consumer.schema.registry.url | ${sys:schema-registry.url}                           |
    And the depot-scans stream of depot-scans kafka events
    And the depot-scans stream kafka event keys are the key metadata
    When the depot-scans stream is published at 200 events per second
    Then the publish completes with no failed events
    And the depot-scans kafka event named out-for-delivery payload properties are:
      | $.status      | OUT_FOR_DELIVERY |
      | $.depot       | DE-HAM-2         |
      | $.weightGrams | 650              |
