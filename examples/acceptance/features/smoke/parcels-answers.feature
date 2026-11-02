Feature: The parcels service answers
  A smoke check after a rollout: the service is alive and wired. It runs alone, in a bubble: no
  run, nothing declared, nothing to clean up.

  Scenario: The parcels service is up
    Given the parcels service with the following properties:
      | url | ${sys:parcels.url} |
    And a GET request to /ping
    When the request is executed
    Then the response status code is 200
