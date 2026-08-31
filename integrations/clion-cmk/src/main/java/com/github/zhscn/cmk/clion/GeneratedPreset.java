package com.github.zhscn.cmk.clion;

import java.util.List;

final class GeneratedPreset {
  private static final String PREFIX = "cmk-";

  private GeneratedPreset() {}

  static boolean isSelected(List<String> parameters) {
    for (int i = 0; i < parameters.size(); i++) {
      String argument = parameters.get(i);
      String value = null;
      if (argument.equals("--preset") && i + 1 < parameters.size()) {
        value = parameters.get(i + 1);
      } else if (argument.startsWith("--preset=")) {
        value = argument.substring("--preset=".length());
      }
      if (value != null && value.startsWith(PREFIX)) {
        return true;
      }
    }
    return false;
  }
}
