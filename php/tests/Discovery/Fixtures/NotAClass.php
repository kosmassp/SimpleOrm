<?php

declare(strict_types=1);

// Deliberately declares no `NotAClass` type: proves ClassScanner::classes()
// only returns names Composer autoload can actually resolve, not every *.php
// file path it walks.
