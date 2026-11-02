@lab @stormbreaker-group/delivery @stormbreaker-requires/parcels,tracking
Feature: Every parcel registered is tracked
  Every parcel becomes a tracking record downstream, through Kafka and a courier nobody in the
  environment owns (mocked). The tracking team's dispatcher derives the records the run will
  create from the parcels declared, and removes them after; the run waits for tracking to consume
  everything before cleanup starts.

  Scenario: Parcels become tracking records
    Given the parcels service with the following properties:
      | url | ${sys:parcels.url} |
    And the events kafka service with the following properties:
      | brokers | ${sys:kafka.brokers} |
    And the parcels stream of POST requests to /parcels
    When the parcels stream is sent at 25 requests per second
    Then the load completes with no failed requests
    And the tracking consumer group catches up on the parcel-events topic within 60s
