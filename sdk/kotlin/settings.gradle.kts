pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositories {
        google()
        mavenCentral()
    }
}

// The project name is the artifact name: a build that includes this one (`includeBuild`) substitutes
// `party.openwatch:openwatchparty` with it.
rootProject.name = "openwatchparty"
