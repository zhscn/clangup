package com.github.zhscn.cmk.clion;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.intellij.execution.process.ProcessOutput;
import java.util.List;
import org.junit.jupiter.api.Test;

final class CmkRunnerStepTest {
  @Test
  void recognizesGeneratedPresetForms() {
    assertTrue(GeneratedPreset.isSelected(List.of("--preset", "cmk-default")));
    assertTrue(GeneratedPreset.isSelected(List.of("--preset=cmk-asan.x")));
  }

  @Test
  void ignoresUnmanagedPresets() {
    assertFalse(GeneratedPreset.isSelected(List.of("--preset", "project-default")));
    assertFalse(GeneratedPreset.isSelected(List.of("--fresh")));
  }

  @Test
  void preservesStandardOutputAndError() {
    ProcessOutput output = new ProcessOutput("configure output\n", "cmk detail\n", 1, false, false);

    assertEquals(
        "\nstdout:\nconfigure output\nstderr:\ncmk detail", CmkRunnerStep.formatOutput(output));
  }
}
