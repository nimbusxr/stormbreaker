Feature: The UI shows the environment as it is
  The stormbreaker UI reads the orchestrator and draws what it reads: its groups, its dispatchers
  and their instances, and its runs. Nothing on the page is drawn by hand.

  Background:
    Given the stormbreaker web app with the following properties:
      | url | ${sys:ui.url} |

  Scenario: Every dispatcher instance, with its lease
    When the "/" page is opened
    Then within 10s the page shows a table row where:
      | Dispatcher | parcels  |
      | Instance   | east     |
      | Groups     | delivery |
      | Lease      | live     |
    And the page shows a table row where:
      | Dispatcher | parcels |
      | Instance   | west    |
      | Lease      | live    |
    And the page shows a table row where:
      | Dispatcher | tracking |
      | Instance   | shared   |
      | Lease      | live     |

  Scenario: Each group, drawn with its dispatchers and their instances
    When the "/" page is opened
    Then within 10s the page shows "delivery: parcels (east, west), tracking (shared)."

  Scenario: Older runs, a page at a time
    Given the "/?per=2" page is opened
    When the "Older →" link is clicked
    Then within 10s the page shows "page 2"

  Scenario: Back to newer runs
    Given the "/?per=2" page is opened
    And the "Older →" link is clicked
    When the "← Newer" link is clicked
    Then within 10s the page shows "page 1"
