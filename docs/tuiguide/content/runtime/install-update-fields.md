# Runtime Install and Update Fields

## Managed Install

### Allow development build

Allows installation of a development/non-release build where the normal managed-install validation would otherwise reject it. This is an explicit opt-in intended for development/testing workflows.

### Clean verified legacy installations

When enabled, the install flow cleans verified legacy standalone installations as part of migration to the managed layout. The default draft enables this cleanup.

Submitting the editor installs the current binary into the managed versioned layout using these options. Errors keep the draft open.

## Update

### Target version

Optional release version to install. Blank selects the latest available verified release. Supplying an explicit version may intentionally select an older version/downgrade, so the label calls out that behavior.

### Skip managed runtime restart

When enabled, applying the update does not restart the managed runtime automatically after installation. Use it when restart timing must be controlled separately.

The update operation verifies/downloads/installs the selected release through the application's update workflow. Progress remains an operation overlay; failures return to the editor with the draft intact.
